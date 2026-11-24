// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"
)

func TestMCPJSONEscapesTerminalControlCharacters(t *testing.T) {
	const value = "검색\x1b[2J\u009b0m\u202e reversed"
	var output bytes.Buffer
	if err := writeMCPJSON(&output, value); err != nil {
		t.Fatal(err)
	}
	for _, control := range []rune{'\x1b', '\u009b', '\u202e'} {
		if bytes.ContainsRune(output.Bytes(), control) {
			t.Fatalf("unescaped terminal control %U", control)
		}
	}
	var decoded string
	if err := json.Unmarshal(output.Bytes(), &decoded); err != nil || decoded != value {
		t.Fatal("escaping changed the original JSON value")
	}
}

func TestMCPArgumentReadCancelsWhileWaitingForStandardInput(t *testing.T) {
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer read.Close()
	defer write.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	if _, err := readMCPArguments(ctx, "-", read); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("input wait must honor cancellation: %v", err)
	}
}
