package main

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/charmbracelet/colorprofile"
	"os"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/wooinwoo/freeway-cli/cli/client"
	"github.com/wooinwoo/freeway-cli/cli/tui"
	"github.com/wooinwoo/freeway-cli/cli/update"
)

var version = "dev"

func main() {
	if handled, code, err := update.Forward(os.Args[1:]); handled {
		if err != nil {
			fmt.Fprintln(os.Stderr, "Freeway:", err)
		}
		os.Exit(code)
	}
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "Freeway:", err)
		os.Exit(1)
	}
}

func run() error {
	if len(os.Args) == 2 && (os.Args[1] == "--version" || os.Args[1] == "version") {
		fmt.Println(version)
		return nil
	}
	if handled, err := runNativeCommand(os.Args[1:]); handled {
		return err
	}
	inv, err := parseInvocation(os.Args[1:])
	if err != nil {
		return err
	}
	if inv.help {
		fmt.Print(usage)
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	model := tui.New()
	if !inv.preview {
		address := inv.url
		if address == "" {
			address = os.Getenv("FREEWAY_URL")
		}
		gateway, err := client.NewHTTP(client.Config{URL: address, APIKey: os.Getenv("FREEWAY_API_KEY")})
		if err != nil {
			return err
		}
		defer func() {
			cancel()
			gateway.Close()
		}()
		if inv.mode == "sessions" {
			records, err := gateway.Conversations(ctx)
			if err != nil {
				return err
			}
			cwd, err := os.Getwd()
			if err != nil {
				return err
			}
			records = client.ScopeConversations(records, cwd, inv.all)
			if inv.json {
				encoder := json.NewEncoder(os.Stdout)
				encoder.SetIndent("", "  ")
				return encoder.Encode(records)
			}
			if len(records) == 0 {
				fmt.Println("No saved sessions in this project. Use freeway sessions --all to see other projects.")
				return nil
			}
			fmt.Printf("%-10s %-16s %-8s %s\n", "SESSION", "UPDATED", "MESSAGES", "TITLE")
			for _, record := range records {
				updated := record.UpdatedAt
				if parsed, err := time.Parse(time.RFC3339, updated); err == nil {
					updated = parsed.Local().Format("01/02 15:04")
				}
				id := record.ID
				if len(id) > 8 {
					id = id[:8]
				}
				title := record.Title
				if record.HasDraft {
					title += " [draft]"
				}
				if record.QueuedCount > 0 {
					title += fmt.Sprintf(" [%d queued]", record.QueuedCount)
				}
				fmt.Printf("%-10s %-16s %-8d %s\n", id, updated, record.MessageCount, title)
			}
			fmt.Println("\nResume: freeway resume SESSION_ID")
			return nil
		}
		model = tui.NewConnected(ctx, gateway).WithStartMode(inv.mode, inv.query, inv.all, inv.noSave)
	}
	options := []tea.ProgramOption{tea.WithFPS(30)}
	if inv.noColor {
		options = append(options, tea.WithColorProfile(colorprofile.NoTTY))
	} else if os.Getenv("WT_SESSION") != "" {
		options = append(options, tea.WithColorProfile(colorprofile.TrueColor))
	}
	_, err = tea.NewProgram(model, options...).Run()
	return err
}
