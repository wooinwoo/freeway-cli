package providers

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

// Adapter owns a vendor protocol, never routing, credentials, or quota storage.
type Adapter interface {
	Models(context.Context, string) ([]Model, error)
	Stream(context.Context, Model, string, Request, func(string)) (Completion, error)
	Close() error
}

type Definition struct {
	Provider
	BaseURL           string         `json:"base"`
	Catalog           bool           `json:"catalog"`
	Seed              []string       `json:"seed,omitempty"`
	Proof             string         `json:"freeBasis"`
	NeedsConfirmation bool           `json:"needsConfirmation"`
	Priority          int            `json:"priority"`
	Limits            map[string]int `json:"limits"`
}

type CLIConfig struct {
	Binary     string
	WorkDir    string
	HTTPClient *http.Client
}

func failure(code string, status int) *Error { return &Error{Code: code, Status: status} }

// readSSE supports CRLF and multiline data fields and bounds each event to 2 MiB.
func readSSE(reader io.Reader, consume func([]byte) error) error {
	const maxEventSize = 2 * 1024 * 1024
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 4096), maxEventSize)
	var event strings.Builder
	flush := func() error {
		if event.Len() == 0 {
			return nil
		}
		err := consume([]byte(strings.TrimSuffix(event.String(), "\n")))
		event.Reset()
		return err
	}
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if err := flush(); err != nil {
				return err
			}
			continue
		}
		if strings.HasPrefix(line, "data:") {
			value := strings.TrimPrefix(line, "data:")
			value = strings.TrimPrefix(value, " ")
			if event.Len()+len(value)+1 > maxEventSize {
				return failure("invalid_stream", 502)
			}
			event.WriteString(value)
			event.WriteByte('\n')
		}
	}
	if err := scanner.Err(); err != nil {
		return failure("invalid_stream", 502)
	}
	return flush()
}

func rawJSON(value any) json.RawMessage {
	if value == nil {
		return nil
	}
	data, _ := json.Marshal(value)
	return data
}
