package assistant

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestValidImageDataAcceptsSupportedImageSignatures(t *testing.T) {
	cases := []struct {
		name   string
		prefix string
		data   []byte
	}{
		{"png", "data:image/png;base64,", []byte("\x89PNG\r\n\x1a\n")},
		{"jpeg", "data:image/jpeg;base64,", []byte{0xff, 0xd8, 0xff}},
		{"webp", "data:image/webp;base64,", []byte("RIFF\x04\x00\x00\x00WEBP")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			value := tc.prefix + base64.StdEncoding.EncodeToString(tc.data)
			if !validImageData(value) {
				t.Fatal("valid signature rejected")
			}
		})
	}
}

func TestValidImageDataRejectsMalformedDataURLs(t *testing.T) {
	cases := []string{
		"data:image/png;base64,%%%",
		"data:image/png;base64,\n" + base64.StdEncoding.EncodeToString([]byte("\x89PNG\r\n\x1a\n")),
		"data:image/png;base64," + base64.StdEncoding.EncodeToString([]byte("not png")),
		"data:image/gif;base64," + base64.StdEncoding.EncodeToString([]byte("GIF89a")),
		"data:image/jpeg;base64," + base64.StdEncoding.EncodeToString([]byte("\x89PNG\r\n\x1a\n")),
	}
	for _, value := range cases {
		if validImageData(value) {
			t.Errorf("accepted invalid image data URL %q", value[:min(len(value), 40)])
		}
	}
}

func TestValidImageDataRejectsOversizedPayload(t *testing.T) {
	value := "data:image/png;base64," + strings.Repeat("A", base64.StdEncoding.EncodedLen(maxImageBytes)+4)
	if validImageData(value) {
		t.Fatal("accepted payload larger than 12MB")
	}
}
