package imagegen

import (
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestImageIntentRoutesOnlyImageRequests(t *testing.T) {
	for _, tc := range []struct {
		input string
		auto  bool
		want  string
	}{
		{input: "검은 배경의 고양이 로고 만들어줘", auto: true, want: "검은 배경의 고양이 로고 만들어줘"},
		{input: "검은 배경의 고양이로고를한장만들어줘", auto: true, want: "검은 배경의 고양이로고를한장만들어줘"},
		{input: "고양이를 이미지로 만들어 주세요", auto: true, want: "고양이를 이미지로 만들어 주세요"},
		{input: "고양이 이미지를 만들어 주실 수 있나요?", auto: true, want: "고양이 이미지를 만들어 주실 수 있나요?"},
		{input: "우주복 입은 강아지 그려줄 수 있어요?", auto: true, want: "우주복 입은 강아지 그려줄 수 있어요?"},
		{input: "'달빛'이라고 적힌 포스터 만들어줘", auto: true, want: "'달빛'이라고 적힌 포스터 만들어줘"},
		{input: "‘달빛’이라고 써 있는 포스터 만들어줘", auto: true, want: "‘달빛’이라고 써 있는 포스터 만들어줘"},
		{input: "Please generate a poster for a show", auto: true, want: "Please generate a poster for a show"},
		{input: "Draw a fox", auto: true, want: "Draw a fox"},
		{input: "Could you please draw a cat?", auto: true, want: "Could you please draw a cat?"},
		{input: "Draw a fox, don't use red", auto: true, want: "Draw a fox, don't use red"},
		{input: `Design an icon for my app with "Hello" underneath`, auto: true, want: `Design an icon for my app with "Hello" underneath`},
		{input: "Draw a logo with 'Hello' across the top", auto: true, want: "Draw a logo with 'Hello' across the top"},
		{input: "고양이 이미지를 생성하는 코드 써줘", auto: true},
		{input: "이미지 생성해 보는 방법을 알려줘", auto: true},
		{input: "이용 흐름을 순서도로 그려줘", auto: true},
		{input: "매출 그래프 그려줘", auto: true},
		{input: "Draw a chart of monthly sales", auto: true},
		{input: "Draw an image of the login flow as Mermaid code", auto: true},
		{input: "Create a logo for my brand, but don't generate an image. I only want a description.", auto: true},
		{input: "Create a logo for my company. Do not generate it yet. Just describe your idea.", auto: true},
		{input: "Illustrate how to write this API", auto: true},
		{input: "Write a tutorial to create an image", auto: true},
		{input: "Can this model generate images?", auto: true},
		{input: "Create an image uploader with authentication", auto: true},
		{input: "Create a database table for recording image metadata", auto: true},
		{input: "문서에 ‘고양이 로고 만들어줘’라는 문장을 넣어줘", auto: true},
		{input: `문서에 "고양이 그림 그려줘`, auto: true},
		{input: "문서에 고양이 그림 그려줘 라고 적어줘", auto: true},
		{input: "문서에 고양이 그림 그려줘 라는 문장을 넣어줘", auto: true},
		{input: "> 고양이 그림 그려줘\n이 문장은 자연스러워?", auto: true},
		{input: "이미지를 만들지 말고 다음 문장을 요약해줘: 고양이 로고 만들어줘", auto: true},
		{input: "이미지를 만들지말고 고양이로고 만들어줘", auto: true},
		{input: "이 사진 설명해줘", auto: true},
		{input: "Create a picture", auto: false},
		{input: "/image   로고 프롬프트 그대로  ", auto: false, want: "로고 프롬프트 그대로"},
		{input: "/image\tdraw a chart", want: "draw a chart"},
		{input: "/image-open", auto: true},
		{input: "/rename 우주복 입은 강아지 그려줘", auto: true},
	} {
		prompt, ok := Prompt(tc.input, tc.auto)
		if ok != (tc.want != "") || prompt != tc.want {
			t.Errorf("Prompt(%q, %t) = %q, %t; want %q", tc.input, tc.auto, prompt, ok, tc.want)
		}
	}
}

func TestMCPRejectsUnsafeEndpointAndUnexpectedImage(t *testing.T) {
	for _, endpoint := range []string{
		"http://example.com/mcp", "file:///tmp/example", "https://user:password@example.com/mcp", "https://example.com/mcp?key=token",
	} {
		if validEndpoint(endpoint) {
			t.Errorf("unsafe endpoint accepted: %q", endpoint)
		}
	}
	if !validEndpoint("https://example.com/mcp") || !validEndpoint("http://127.0.0.1:4142/mcp") {
		t.Fatal("valid endpoints rejected")
	}
	if _, err := imageExtension(&mcp.ImageContent{MIMEType: "image/png", Data: []byte("<script>")}); err == nil {
		t.Fatal("non-image tool output was accepted")
	}
	if Find("../other") != "" || Find(strings.Repeat("a", 129)) != "" {
		t.Fatal("invalid image path identity was accepted")
	}
}
