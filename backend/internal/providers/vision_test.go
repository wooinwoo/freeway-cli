package providers

import (
	"encoding/json"
	"testing"
)

func TestImagesAreSentAsContentPartsAndCatalogsMarkImageInput(t *testing.T) {
	plain, _ := json.Marshal(Message{Role: "user", Content: "hi"})
	withImage, _ := json.Marshal(Message{Role: "user", Content: "what is this?", Images: []string{"data:image/png;base64,AAAA"}})
	if string(plain) != `{"role":"user","content":"hi"}` {
		t.Fatalf("plain message = %s", plain)
	}
	if want := `{"content":[{"text":"what is this?","type":"text"},{"image_url":{"url":"data:image/png;base64,AAAA"},"type":"image_url"}],"role":"user"}`; string(withImage) != want {
		t.Fatalf("image message = %s", withImage)
	}
	for raw, want := range map[string]bool{
		`{"architecture":{"input_modalities":["text","image"]}}`: true,
		`{"modalities":{"input":["text","image"]}}`:              true,
		`{"architecture":{"input_modalities":["text"]}}`:         false,
		`{}`: false,
	} {
		var entry map[string]any
		_ = json.Unmarshal([]byte(raw), &entry)
		if readsImages(entry) != want {
			t.Fatalf("%s: readsImages = %v", raw, !want)
		}
	}
}
