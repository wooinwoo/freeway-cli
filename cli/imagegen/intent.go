// SPDX-License-Identifier: MIT

package imagegen

import (
	"regexp"
	"strings"
	"unicode"
)

var (
	englishImage = regexp.MustCompile(englishRequestStart + `(generate|draw|create|make|render|design|illustrate) (me )?((a|an|the|one|some) )?([a-z0-9-]+ ){0,3}(image|picture|photo|illustration|poster|logo|drawing|wallpaper|icon|artwork)($|[.,!?]| (of|for|with|showing|featuring|depicting|about|in|on|from|based|using|please)\b)`)
	englishDraw  = regexp.MustCompile(englishRequestStart + `(draw|illustrate) [a-z0-9]`)
	englishText  = regexp.MustCompile(`\b(` +
		`(do not|don['’]t|never) (generate|draw|create|make|render|produce)(( [a-z0-9-]+){0,3} (images?|pictures?|photos?|illustrations?|posters?|logos?|artwork)\b|( (it|anything))?( (now|yet))?($|[.,;!?]))` +
		`|(in|as) (mermaid|markdown|svg|ascii|[a-z0-9+#-]+ code|code)\b|only want (text|a description|a prompt)\b)`)
	koreanImage = regexp.MustCompile(`(이미지|그림|로고|일러스트|사진|포스터|아이콘|배경\s*화면)(을|를|으?로)?\s*(한\s*장|1\s*장|하나|좀)?\s*(그려|만들어|생성해|제작해|그려서\s*보여)` + koreanRequestEnd)
	koreanDraw  = regexp.MustCompile(`그려\s*` + koreanPoliteEnd + `($|[.!?…])`)
	koreanQuote = regexp.MustCompile(`^\s*(라고\s*(적어|써|넣어|입력해|말해|따라해)|라는\s*(문장|문구|말))`)
	textNoun    = regexp.MustCompile(`\b(uploader|generator|workflow|website|webpage|database|tables?|metadata|app|button|form|scripts?|api|component|summari[sz]e|instead|graphs?|charts?|diagrams?|flowcharts?|mermaid|tutorial|code|instructions?|example|how|why|prompt)\b`)
	quotedText  = regexp.MustCompile("\"[^\"]*(\"|$)|“[^”]*(”|$)|‘[^’]*(’|$)|`[^`]*(`|$)|(^| )'[^']*('|$)")
	quotedLine  = regexp.MustCompile(`(?m)^\s*>[^\n]*`)
)

const (
	englishRequestStart = `^(please )?((can|could|would) you (please )?)?`
	koreanPoliteEnd     = `(줘요?|주세요|주시겠어요|줄래|주실래요?|(?:줄|주실)\s*수\s*있(?:어요?|나요|습니까)|주겠어)`
	koreanRequestEnd    = `(\s*` + koreanPoliteEnd + `($|[\s.!?…,])|$|[.!?…,])`
)

// Prompt identifies explicit image requests and common Korean and English requests in Auto.
// Ambiguous requests keep their normal text path. /image is an explicit override.
func Prompt(text string, auto bool) (string, bool) {
	if rest, explicit := strings.CutPrefix(text, "/image"); explicit {
		prompt := strings.TrimLeftFunc(rest, unicode.IsSpace)
		if prompt != rest {
			return strings.TrimSpace(prompt), true
		}
	}
	if !auto || strings.HasPrefix(strings.TrimLeftFunc(text, unicode.IsSpace), "/") {
		return "", false
	}
	normalized := strings.ToLower(strings.Join(strings.Fields(quotedLine.ReplaceAllString(text, " ")), " "))
	normalized = strings.Join(strings.Fields(quotedText.ReplaceAllString(normalized, " ")), " ")
	if englishText.MatchString(normalized) {
		return "", false
	}
	for _, textRequest := range []string{
		"설명해", "분석해", "번역해", "요약", "프롬프트", "아이디어", "코드", "스크립트", "하는 법", "하는법", "방법",
		"만들어야", "생성해야", "만들지 말", "만들지말", "생성하지", "그리지", "업로더", "업로드 기능",
	} {
		if strings.Contains(normalized, textRequest) {
			return "", false
		}
	}
	if head := englishImage.FindString(normalized); head != "" && !textNoun.MatchString(head) {
		return text, true
	}
	if englishDraw.MatchString(normalized) && !textNoun.MatchString(normalized) {
		return text, true
	}
	if match := koreanImage.FindStringIndex(normalized); match != nil && !koreanQuote.MatchString(normalized[match[1]:]) {
		return text, true
	}
	if match := koreanDraw.FindStringIndex(normalized); match != nil && !koreanQuote.MatchString(normalized[match[1]:]) {
		for _, diagram := range []string{"그래프", "차트", "순서도", "다이어그램", "표로", "도식"} {
			if strings.Contains(normalized, diagram) {
				return "", false
			}
		}
		return text, true
	}
	return "", false
}
