// SPDX-License-Identifier: MIT

package tui

// The free models cannot open links or files themselves, so Freeway reads the links and
// local files a prompt names and sends their text with it as reference data.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"syscall"
	"time"
	"unicode"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"github.com/wooinwoo/freeway-cli/cli/client"
)

// A web page keeps its first 24 KB of text, enough for an article and cheap for free quotas.
const maxLinkText = 24 * 1024

var (
	linkPattern  = regexp.MustCompile(`https?://[^\s<>"'` + "`" + `]+`)
	tokenPattern = regexp.MustCompile(`'([^'\n]+)'|"([^"\n]+)"|(\S+)`)
	drivePath    = regexp.MustCompile(`^[A-Za-z]:\\`)
	// Files that commonly hold keys or passwords are never read from a prompt.
	secretName = regexp.MustCompile(`(?i)^(\.env(\..*)?|id_(rsa|dsa|ecdsa|ed25519)(\.pub)?|.*\.(pem|key|p12|pfx|kdbx)|\.netrc|\.pgpass|credentials(\.json)?)$`)
	secretDirs = []string{".ssh", ".aws", ".gnupg", ".kube", ".docker"}
	errPrivate = errors.New("links to this computer or a private network are not read")
)

type referencesRead struct {
	generation uint64
	index      int
	files      []client.Attachment
	problems   []string
}

// trimReference drops punctuation and Korean particles written right after a link or path,
// as in "README.md를" or "https://example.com/a).".
func trimReference(value string) string {
	return strings.TrimRightFunc(value, func(r rune) bool {
		return strings.ContainsRune(".,;:!?)]}>'\"…", r) || unicode.Is(unicode.Hangul, r)
	})
}

// promptLinks returns the web links in a prompt in the order written.
func promptLinks(text string) []string {
	var links []string
	for _, found := range linkPattern.FindAllString(text, -1) {
		if link := trimReference(found); len(link) > len("https://") && len(link) <= 1000 {
			links = append(links, link)
		}
	}
	return links
}

// promptPaths returns existing regular files a prompt names, as absolute paths.
// Quoted paths may contain spaces, as terminals write dropped files.
func promptPaths(text, root string) []string {
	home, _ := os.UserHomeDir()
	var paths []string
	for _, match := range tokenPattern.FindAllStringSubmatch(text, -1) {
		token := match[1] + match[2] + match[3]
		if strings.Contains(token, "://") || !strings.ContainsAny(token, `./\`) {
			continue
		}
		for _, candidate := range []string{token, trimReference(token)} {
			if path := resolvePromptPath(candidate, root, home); path != "" {
				paths = append(paths, path)
				break
			}
		}
	}
	return paths
}

func resolvePromptPath(value, root, home string) string {
	switch {
	case value == "":
		return ""
	case strings.HasPrefix(value, "~/") && home != "":
		value = filepath.Join(home, value[2:])
	case drivePath.MatchString(value) && runtime.GOOS == "linux":
		// A file dropped from Windows into WSL arrives as C:\..., which WSL mounts at /mnt/c.
		value = "/mnt/" + strings.ToLower(value[:1]) + "/" + strings.ReplaceAll(value[3:], `\`, "/")
	case !filepath.IsAbs(value):
		value = filepath.Join(root, value)
	}
	info, err := os.Stat(value)
	if err != nil || !info.Mode().IsRegular() {
		return ""
	}
	return filepath.Clean(value)
}

// pendingReferences lists the links and files in a prompt that are not attached yet.
func pendingReferences(message client.Message, root string) (links, paths []string) {
	attached := map[string]bool{}
	for _, file := range message.Files {
		attached[file.Path] = true
	}
	for _, link := range promptLinks(message.Content) {
		if !attached[link] {
			attached[link] = true
			links = append(links, link)
		}
	}
	for _, path := range promptPaths(message.Content, root) {
		if label := referencePathLabel(path, root); !attached[label] {
			attached[label] = true
			paths = append(paths, path)
		}
	}
	return links, paths
}

// referencePathLabel names a file the way the @ picker does inside the project, and by its full path elsewhere.
func referencePathLabel(path, root string) string {
	if rel, err := filepath.Rel(root, path); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel) {
		return filepath.ToSlash(rel)
	}
	return path
}

func (m Model) readReferences(index int, links, paths []string) (tea.Model, tea.Cmd) {
	m.generation++
	generation := m.generation
	ctx, cancel := context.WithCancel(m.ctx)
	m.cancel = cancel
	m.status = "Reading links and files"
	m.renderMessages()
	room := max(0, 5-len(m.messages[index].Files))
	root := m.workdir
	tr := m.tr
	language := tr("en", "ko,en;q=0.8")
	full := tr("five attachments per prompt", "질문당 첨부는 5개까지")
	return m, func() tea.Msg {
		result := referencesRead{generation: generation, index: index}
		for _, path := range paths {
			label := referencePathLabel(path, root)
			if len(result.files) == room {
				result.problems = append(result.problems, label+": "+full)
				continue
			}
			read := readReferenceFile
			if isImagePath(path) {
				read = imageMarker
			}
			content, err := read(path, tr)
			if err != nil {
				result.problems = append(result.problems, label+": "+err.Error())
				continue
			}
			result.files = append(result.files, client.Attachment{Path: label, Content: content})
		}
		for _, link := range links {
			if len(result.files) == room {
				result.problems = append(result.problems, link+": "+full)
				continue
			}
			content, err := readLink(ctx, link, language, tr)
			if err != nil {
				if ctx.Err() != nil {
					return result
				}
				result.problems = append(result.problems, link+": "+err.Error())
				continue
			}
			result.files = append(result.files, client.Attachment{Path: link, Content: content})
		}
		return result
	}
}

func (m Model) finishReferences(msg referencesRead) (tea.Model, tea.Cmd) {
	if !m.busy || msg.generation != m.generation || msg.index >= len(m.messages) {
		return m, nil
	}
	m.messages[msg.index].Files = append(m.messages[msg.index].Files, msg.files...)
	if len(msg.problems) > 0 {
		problem := msg.problems[0]
		if len(msg.problems) > 1 {
			problem += fmt.Sprintf(m.tr(" (and %d more)", " 외 %d개"), len(msg.problems)-1)
		}
		m.notify(m.tr("Could not read ", "읽지 못함 · ") + problem)
	}
	m.status = "Preparing response"
	m.dirty = true
	return m.beginTextStream(m.responseTarget.model, m.responseContext().messages(), "")
}

func readReferenceFile(path string, tr func(string, string) string) (string, error) {
	parts := strings.Split(filepath.ToSlash(path), "/")
	for _, dir := range secretDirs {
		if len(parts) > 1 && slices.Contains(parts[:len(parts)-1], dir) {
			return "", errors.New(tr("files in "+dir+" may hold keys and are not read", dir+" 안의 파일은 키가 들어 있을 수 있어 읽지 않음"))
		}
	}
	if secretName.MatchString(filepath.Base(path)) {
		return "", errors.New(tr("this file may hold keys or passwords and is not read", "키나 비밀번호가 들어 있을 수 있는 파일이라 읽지 않음"))
	}
	return readTextFile(path)
}

func linkClient() *http.Client {
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	proxied := false
	for _, name := range []string{"HTTPS_PROXY", "https_proxy", "HTTP_PROXY", "http_proxy", "ALL_PROXY", "all_proxy"} {
		proxied = proxied || os.Getenv(name) != ""
	}
	if !proxied {
		// Checked on the address actually dialed, so a name that resolves to a local address is refused too.
		// Behind a proxy the proxy decides where requests go.
		dialer.Control = func(_, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return errPrivate
			}
			ip, err := netip.ParseAddr(host)
			if ip = ip.Unmap(); err != nil || !ip.IsGlobalUnicast() || ip.IsPrivate() || netip.MustParsePrefix("100.64.0.0/10").Contains(ip) {
				return errPrivate
			}
			return nil
		}
	}
	return &http.Client{Timeout: 20 * time.Second, Transport: &http.Transport{
		Proxy: http.ProxyFromEnvironment, DialContext: dialer.DialContext,
		TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: 15 * time.Second,
	}}
}

func readLink(ctx context.Context, link, language string, tr func(string, string) string) (string, error) {
	private := errors.New(tr(errPrivate.Error(), "이 컴퓨터나 내부망 주소는 읽지 않음"))
	target, err := url.Parse(link)
	if err != nil || (target.Scheme != "http" && target.Scheme != "https") || target.Hostname() == "" {
		return "", errors.New(tr("not a web link", "웹 링크가 아님"))
	}
	if ip, err := netip.ParseAddr(target.Hostname()); err == nil && (ip.IsLoopback() || ip.IsPrivate()) || target.Hostname() == "localhost" {
		return "", private
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, link, nil)
	if err != nil {
		return "", err
	}
	request.Header.Set("User-Agent", "Mozilla/5.0 (compatible; Freeway)")
	request.Header.Set("Accept", "text/html,application/xhtml+xml,text/plain,application/json;q=0.9,*/*;q=0.5")
	request.Header.Set("Accept-Language", language)
	response, err := linkClient().Do(request)
	if err != nil {
		if errors.Is(err, errPrivate) {
			return "", private
		}
		return "", errors.New(tr("the site did not respond", "사이트가 응답하지 않음"))
	}
	defer response.Body.Close()
	if response.StatusCode >= 400 {
		return "", fmt.Errorf(tr("the site answered HTTP %d", "사이트가 HTTP %d로 거절함"), response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 4<<20))
	if err != nil {
		return "", errors.New(tr("the page stopped loading", "페이지를 끝까지 받지 못함"))
	}
	media, _, _ := mime.ParseMediaType(response.Header.Get("Content-Type"))
	var text string
	switch {
	case media == "text/html" || media == "application/xhtml+xml" || media == "" && strings.Contains(strings.ToLower(string(body[:min(len(body), 512)])), "<html"):
		text = pageText(string(body), response.Request.URL)
	case strings.HasPrefix(media, "text/") || strings.HasSuffix(media, "json") || strings.HasSuffix(media, "xml"):
		text = string(body)
	case media == "application/pdf":
		return "", errors.New(tr("PDF files cannot be read yet", "PDF는 아직 읽을 수 없음"))
	default:
		return "", errors.New(tr(media+" is not text", media+" 형식은 글자가 아님"))
	}
	if !utf8.ValidString(text) {
		return "", errors.New(tr("only UTF-8 pages can be read", "UTF-8 페이지만 읽을 수 있음"))
	}
	if text = strings.TrimSpace(text); text == "" {
		return "", errors.New(tr("the page has no readable text; it may need JavaScript", "읽을 글자가 없음. 자바스크립트로만 보이는 페이지일 수 있음"))
	}
	if len(text) > maxLinkText {
		cut := maxLinkText
		for cut > 0 && !utf8.RuneStart(text[cut]) {
			cut--
		}
		text = text[:cut] + "\n[Freeway kept only the beginning of this page.]"
	}
	return text, nil
}

var (
	pageTitle   = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)
	pageMeta    = regexp.MustCompile(`(?is)<meta\s[^>]*(?:name|property)=["'](?:og:)?description["'][^>]*content=["']([^"']*)["']`)
	pageDrop    = regexp.MustCompile(`(?is)<!--.*?-->|<(script|style|noscript|svg|template|head|nav|footer|aside|form|iframe)\b.*?</(script|style|noscript|svg|template|head|nav|footer|aside|form|iframe)\s*>`)
	pageItem    = regexp.MustCompile(`(?i)<li\b[^>]*>`)
	pageBreak   = regexp.MustCompile(`(?i)<(br|hr)\b[^>]*>|</(p|div|li|h[1-6]|tr|section|article|blockquote|pre|table|ul|ol|header|main|dd|dt|figcaption)\s*>`)
	pageTag     = regexp.MustCompile(`(?s)<[^>]*>`)
	pageSpace   = regexp.MustCompile(`[ \t\f\v\x{00a0}]+`)
	youtubeData = regexp.MustCompile(`ytInitialPlayerResponse\s*=\s*`)
)

// pageText keeps a page's title, description and visible text.
func pageText(source string, address *url.URL) string {
	if host := strings.TrimPrefix(address.Hostname(), "www."); host == "youtube.com" || host == "m.youtube.com" || host == "youtu.be" {
		if video := youtubeText(source); video != "" {
			return video
		}
	}
	var out strings.Builder
	if match := pageTitle.FindStringSubmatch(source); match != nil {
		fmt.Fprintf(&out, "Title: %s\n", oneLine(html.UnescapeString(match[1])))
	}
	if match := pageMeta.FindStringSubmatch(source); match != nil && strings.TrimSpace(match[1]) != "" {
		fmt.Fprintf(&out, "Description: %s\n", oneLine(html.UnescapeString(match[1])))
	}
	fmt.Fprintf(&out, "Address: %s\n\n", address)
	body := pageDrop.ReplaceAllString(source, " ")
	body = pageItem.ReplaceAllString(body, "\n• ")
	body = pageBreak.ReplaceAllString(body, "\n")
	body = html.UnescapeString(pageTag.ReplaceAllString(body, " "))
	blank := false
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(pageSpace.ReplaceAllString(line, " "))
		if line == "" || line == "•" {
			blank = true
			continue
		}
		if blank && out.Len() > 0 {
			out.WriteString("\n")
		}
		out.WriteString(line + "\n")
		blank = false
	}
	return out.String()
}

// youtubeText reads a video's title, channel and description from its public page.
// The page offers no captions without a signed-in browser, so the model is told so.
func youtubeText(source string) string {
	start := youtubeData.FindStringIndex(source)
	if start == nil {
		return ""
	}
	var player struct {
		VideoDetails struct {
			Title         string `json:"title"`
			Author        string `json:"author"`
			LengthSeconds string `json:"lengthSeconds"`
			Description   string `json:"shortDescription"`
		} `json:"videoDetails"`
	}
	if json.NewDecoder(strings.NewReader(source[start[1]:])).Decode(&player) != nil || player.VideoDetails.Title == "" {
		return ""
	}
	video := player.VideoDetails
	var seconds int
	fmt.Sscan(video.LengthSeconds, &seconds)
	return fmt.Sprintf("YouTube video\nTitle: %s\nChannel: %s\nLength: %d:%02d\n\nDescription:\n%s\n\n"+
		"[Freeway could read only the title, channel and description. It cannot hear the video or read its captions, so say so if the question needs what is said in the video.]",
		video.Title, video.Author, seconds/60, seconds%60, strings.TrimSpace(video.Description))
}

// imageAttachment stands in for an image in a saved conversation. The picture itself is read
// from disk for the question it belongs to, so saves stay small and earlier images are not resent.
const imageAttachment = "[Image file. Freeway sends the picture only with the question it was attached to.]"

// Most vision routes accept pictures of a few megabytes; two fit the gateway's 8 MiB request.
const maxImageBytes = 2560 * 1024

var imageTypes = map[string]string{".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".webp": "image/webp", ".gif": "image/gif"}

func isImagePath(path string) bool { return imageTypes[strings.ToLower(filepath.Ext(path))] != "" }

func imageMarker(path string, tr func(string, string) string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if info.Size() > maxImageBytes {
		return "", errors.New(tr("images can be at most 2.5 MB", "이미지는 2.5 MB까지 보낼 수 있음"))
	}
	return imageAttachment, nil
}

// withImages reads the pictures attached to the newest question and sends them as images.
// Earlier questions keep only the note that an image was there.
func withImages(request []client.Message, root string) []client.Message {
	last := -1
	for i := range request {
		if request[i].Role == "user" {
			last = i
		}
	}
	if last < 0 {
		return request
	}
	message := request[last]
	var files []client.Attachment
	var images []string
	total := 0
	for _, file := range message.Files {
		if file.Content != imageAttachment {
			files = append(files, file)
			continue
		}
		path := file.Path
		if !filepath.IsAbs(path) {
			path = filepath.Join(root, filepath.FromSlash(path))
		}
		data, err := os.ReadFile(path)
		if err != nil || len(data) > maxImageBytes || total+len(data) > 2*maxImageBytes {
			files = append(files, client.Attachment{Path: file.Path, Content: "[Freeway could not send this image: the file is missing, changed or too large.]"})
			continue
		}
		total += len(data)
		images = append(images, "data:"+imageTypes[strings.ToLower(filepath.Ext(path))]+";base64,"+base64.StdEncoding.EncodeToString(data))
	}
	if len(images) == 0 {
		return request
	}
	request = append([]client.Message(nil), request...)
	message.Files, message.Images = files, images
	request[last] = message
	return request
}
