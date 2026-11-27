package tui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestPromptReferencesDropTrailingParticlesAndPunctuation(t *testing.T) {
	links := promptLinks("이 영상 https://youtu.be/abc를 요약하고 (https://example.com/a). 도 봐줘")
	if want := []string{"https://youtu.be/abc", "https://example.com/a"}; !slices.Equal(links, want) {
		t.Fatalf("links = %q, want %q", links, want)
	}
	root := t.TempDir()
	for _, name := range []string{"notes.md", "two words.txt"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("메모"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	paths := promptPaths(`notes.md를 읽고 "two words.txt"도 봐. /model 명령과 1.5는 파일이 아님`, root)
	if want := []string{filepath.Join(root, "notes.md"), filepath.Join(root, "two words.txt")}; !slices.Equal(paths, want) {
		t.Fatalf("paths = %q, want %q", paths, want)
	}
}

func TestAFileNamedInThePromptIsSentWithIt(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "notes.md"), []byte("회의는 3시"), 0o600); err != nil {
		t.Fatal(err)
	}
	g := &burstGateway{}
	m := connectedFixture(g)
	m.workdir = root
	m.prompt.SetValue("notes.md 요약해줘")
	next, cmd := m.Update(press(tea.KeyEnter))
	m, _ = drainResponse(t, next.(Model), cmd)
	if len(g.requests) != 1 {
		t.Fatalf("the prompt was sent %d times", len(g.requests))
	}
	sent := g.requests[0][len(g.requests[0])-1]
	if len(sent.Files) != 1 || sent.Files[0].Path != "notes.md" || sent.Files[0].Content != "회의는 3시" {
		t.Fatalf("the named file did not go with the prompt: %+v", sent.Files)
	}
	if saved := m.messages[len(m.messages)-2]; len(saved.Files) != 1 {
		t.Fatal("the read file was not kept with the prompt, so a retry would read it again")
	}
}

func TestKeyFilesAndLocalAddressesAreNotRead(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{".env", "id_ed25519", "server.pem"} {
		path := filepath.Join(root, name)
		if err := os.WriteFile(path, []byte("SECRET=1"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := readReferenceFile(path, english); err == nil {
			t.Fatalf("%s was read", name)
		}
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("internal")) }))
	defer server.Close()
	port := strings.TrimPrefix(server.URL, "http://127.0.0.1")
	// The mapped address passes the name check, so only the dial check can refuse it.
	for _, link := range []string{server.URL, "http://localhost" + port, "http://[::ffff:127.0.0.1]" + port} {
		if _, err := readLink(context.Background(), link, "en", english); err == nil || err.Error() != errPrivate.Error() {
			t.Fatalf("%s: err = %v, want the private address refusal", link, err)
		}
	}
}

func TestPageTextKeepsWhatAReaderSees(t *testing.T) {
	page := `<html><head><title>뉴스 &amp; 소식</title><meta name="description" content="요약문"><script>var x=1</script></head>` +
		`<body><nav>메뉴</nav><article><h1>제목</h1><p>첫 문단</p><ul><li>하나</li><li>둘</li></ul></article><footer>저작권</footer></body></html>`
	address, _ := url.Parse("https://example.com/news")
	text := pageText(page, address)
	for _, want := range []string{"Title: 뉴스 & 소식\n", "Description: 요약문\n", "제목\n", "첫 문단\n", "• 하나\n", "• 둘\n"} {
		if !strings.Contains(text, want) {
			t.Fatalf("page text lacks %q:\n%s", want, text)
		}
	}
	for _, hidden := range []string{"var x", "메뉴", "저작권"} {
		if strings.Contains(text, hidden) {
			t.Fatalf("page text kept %q:\n%s", hidden, text)
		}
	}
	video := `<script>var ytInitialPlayerResponse = {"videoDetails":{"title":"노래","author":"채널","lengthSeconds":"213","shortDescription":"설명"}};var meta = 1;</script>`
	address, _ = url.Parse("https://www.youtube.com/watch?v=abc")
	text = pageText(video, address)
	for _, want := range []string{"Title: 노래\n", "Channel: 채널\n", "Length: 3:33\n", "설명", "cannot hear the video"} {
		if !strings.Contains(text, want) {
			t.Fatalf("video text lacks %q:\n%s", want, text)
		}
	}
}

func TestAnImageNamedInThePromptGoesAsAPictureButIsSavedAsANote(t *testing.T) {
	root := t.TempDir()
	png := []byte("\x89PNG\r\n\x1a\nfake")
	if err := os.WriteFile(filepath.Join(root, "shot.png"), png, 0o600); err != nil {
		t.Fatal(err)
	}
	g := &burstGateway{}
	m := connectedFixture(g)
	m.workdir = root
	m.prompt.SetValue("shot.png 이 화면 뭐가 문제야?")
	next, cmd := m.Update(press(tea.KeyEnter))
	m, _ = drainResponse(t, next.(Model), cmd)
	sent := g.requests[0][len(g.requests[0])-1]
	if len(sent.Images) != 1 || sent.Images[0] != "data:image/png;base64,iVBORw0KGgpmYWtl" || len(sent.Files) != 0 {
		t.Fatalf("the image was not sent as a picture: images %q files %+v", sent.Images, sent.Files)
	}
	saved := m.messages[len(m.messages)-2]
	if len(saved.Files) != 1 || saved.Files[0].Content != imageAttachment || len(saved.Images) != 0 {
		t.Fatalf("the saved question holds %+v", saved.Files)
	}
	if !strings.Contains(ansi.Strip(m.View().Content), "[image] shot.png") {
		t.Fatal("the conversation does not show the attached image")
	}
}
