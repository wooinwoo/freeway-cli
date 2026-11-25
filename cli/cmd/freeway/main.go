package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
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
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	prefs := tui.LoadPreferences()
	if len(os.Args) > 1 && os.Args[1] == "image" {
		return runImageCommand(ctx, os.Args[2:], prefs)
	}
	if len(os.Args) > 1 && os.Args[1] == "mcp" {
		return runMCPCommand(ctx, os.Args[2:], prefs)
	}
	if len(os.Args) > 1 && os.Args[1] == "connect" {
		if len(os.Args) > 3 || !interactiveTerminal() {
			return fmt.Errorf("freeway connect [PROVIDER] requires an interactive terminal")
		}
		gateway, closeGateway, err := openGateway(ctx, "", "")
		if err != nil {
			return err
		}
		defer closeGateway()
		providerID := ""
		if len(os.Args) == 3 {
			providerID = os.Args[2]
		}
		_, _, err = setupConnections(ctx, gateway, &prefs, terminalOptions(ctx, prefs.NoColor), "", providerID)
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
	prefs.NoColor = prefs.NoColor || inv.noColor
	options := terminalOptions(ctx, prefs.NoColor)
	model := tui.New()
	if !inv.preview {
		address := inv.url
		if address == "" {
			address = os.Getenv("FREEWAY_URL")
		}
		key := os.Getenv("FREEWAY_API_KEY")
		chatUI := inv.mode != "sessions" && interactiveTerminal()
		if chatUI {
			proceed, err := prefs.ConfigureAppearance(options...)
			if !proceed {
				return err
			}
			if err != nil {
				fmt.Fprintln(os.Stderr, prefs.Text("Could not save language and theme.", "언어와 테마를 저장하지 못했습니다."), err)
			}
		}
		localUI := chatUI && address == "" && key == ""
		if localUI {
			if restarted, err := startupUpdate(ctx, prefs, options); restarted || err != nil {
				return err
			}
		}
		gateway, closeGateway, err := openGateway(ctx, address, key)
		if err != nil {
			return err
		}
		defer func() {
			cancel()
			closeGateway()
		}()
		if inv.mode == "sessions" {
			cwd, err := os.Getwd()
			if err != nil {
				return err
			}
			if inv.all {
				cwd = ""
			}
			records, err := gateway.ScopedConversations(ctx, cwd)
			if err != nil {
				return err
			}
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
		var statuses []client.ProviderStatus
		if localUI {
			mode, readyStatuses, err := setupConnections(ctx, gateway, &prefs, options, inv.mode, "")
			if err != nil || mode == "" {
				return err
			}
			inv.mode = mode
			statuses = readyStatuses
		}
		model = tui.NewConnected(ctx, gateway).WithStartMode(inv.mode, inv.query, inv.all, inv.noSave).WithProviderStatuses(statuses)
		if localUI {
			if executable, err := os.Executable(); err == nil {
				model = model.WithProviderSetup(executable)
			}
		}
	}
	final, err := tea.NewProgram(model.WithPreferences(prefs), options...).Run()
	if chat, ok := final.(tui.Model); ok && err == nil && chat.Resumable() {
		fmt.Fprintln(os.Stderr, prefs.Text("Conversation saved. Continue it with: freeway -c", "대화를 저장했습니다. 이어서 하려면: freeway -c"))
	}
	return err
}

func terminalOptions(ctx context.Context, noColor bool) []tea.ProgramOption {
	options := []tea.ProgramOption{tea.WithContext(ctx), tea.WithFPS(30)}
	if noColor {
		options = append(options, tea.WithColorProfile(colorprofile.NoTTY))
	} else if os.Getenv("WT_SESSION") != "" {
		options = append(options, tea.WithColorProfile(colorprofile.TrueColor))
	}
	return options
}
