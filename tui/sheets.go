package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/option"
	"google.golang.org/api/sheets/v4"
)

type SheetOptions struct {
	SheetID    string
	SheetGID   int64
	SheetName  string
	SheetRange string
	CredsPath  string
	TokenPath  string
}

func LoadPRLinksFromSheet(ctx context.Context, opts SheetOptions) ([]string, error) {
	if opts.SheetID == "" {
		return nil, fmt.Errorf("sheet id is required")
	}

	client, err := sheetsClient(ctx, opts.CredsPath, opts.TokenPath)
	if err != nil {
		return nil, err
	}

	service, err := sheets.NewService(ctx, option.WithHTTPClient(client))
	if err != nil {
		return nil, fmt.Errorf("create sheets service: %w", err)
	}

	sheetName := opts.SheetName
	if sheetName == "" {
		sheetName, err = resolveSheetName(service, opts.SheetID, opts.SheetGID)
		if err != nil {
			return nil, err
		}
	}

	sheetRange := opts.SheetRange
	if sheetRange == "" {
		sheetRange = fmt.Sprintf("%s!A:Z", sheetName)
	}

	resp, err := service.Spreadsheets.Values.Get(opts.SheetID, sheetRange).Do()
	if err != nil {
		return nil, fmt.Errorf("read sheet values: %w", err)
	}
	if len(resp.Values) == 0 {
		return nil, fmt.Errorf("sheet has no data")
	}

	columns, err := resolveColumns(resp.Values[0])
	if err != nil {
		return nil, err
	}

	links := make([]string, 0)
	for _, row := range resp.Values[1:] {
		link := getColumnValue(row, columns.linkIdx)
		if link == "" {
			continue
		}
		takenValue := getColumnValue(row, columns.takenIdx)
		if isTaken(takenValue) {
			continue
		}
		links = append(links, link)
	}

	return links, nil
}

type sheetColumns struct {
	takenIdx int
	linkIdx  int
}

func resolveColumns(header []interface{}) (sheetColumns, error) {
	columns := sheetColumns{takenIdx: -1, linkIdx: -1}
	for i, cell := range header {
		value := strings.TrimSpace(fmt.Sprintf("%v", cell))
		lower := strings.ToLower(value)
		switch lower {
		case "taken":
			columns.takenIdx = i
		case "pr_link", "pr link", "pr":
			columns.linkIdx = i
		}
	}
	if columns.takenIdx == -1 || columns.linkIdx == -1 {
		return sheetColumns{}, fmt.Errorf("missing required columns: taken, pr_link")
	}
	return columns, nil
}

func resolveSheetName(service *sheets.Service, sheetID string, gid int64) (string, error) {
	spreadsheet, err := service.Spreadsheets.Get(sheetID).Fields("sheets(properties(title,sheetId))").Do()
	if err != nil {
		return "", fmt.Errorf("get spreadsheet: %w", err)
	}
	if len(spreadsheet.Sheets) == 0 {
		return "", fmt.Errorf("no sheets found")
	}
	if gid > 0 {
		for _, sheet := range spreadsheet.Sheets {
			if sheet.Properties != nil && sheet.Properties.SheetId == gid {
				return sheet.Properties.Title, nil
			}
		}
		return "", fmt.Errorf("sheet gid %d not found", gid)
	}
	return spreadsheet.Sheets[0].Properties.Title, nil
}

func getColumnValue(row []interface{}, idx int) string {
	if idx < 0 || idx >= len(row) {
		return ""
	}
	return strings.TrimSpace(fmt.Sprintf("%v", row[idx]))
}

func isTaken(value string) bool {
	if value == "" {
		return false
	}
	normalized := strings.ToLower(strings.TrimSpace(value))
	switch normalized {
	case "true", "yes", "y", "1", "taken":
		return true
	default:
		return false
	}
}

func sheetsClient(ctx context.Context, credsPath, tokenPath string) (*http.Client, error) {
	if credsPath == "" {
		return nil, fmt.Errorf("google oauth credentials file is required")
	}

	credsData, err := os.ReadFile(credsPath)
	if err != nil {
		return nil, fmt.Errorf("read credentials: %w", err)
	}

	config, err := google.ConfigFromJSON(credsData, sheets.SpreadsheetsReadonlyScope)
	if err != nil {
		return nil, fmt.Errorf("parse credentials: %w", err)
	}

	if tokenPath == "" {
		configDir, err := os.UserConfigDir()
		if err != nil {
			return nil, fmt.Errorf("user config dir: %w", err)
		}
		tokenPath = filepath.Join(configDir, "pr-filter", "token.json")
	}

	client, err := getClient(ctx, config, tokenPath)
	if err != nil {
		return nil, err
	}
	return client, nil
}

func getClient(ctx context.Context, config *oauth2.Config, tokenPath string) (*http.Client, error) {
	if token, err := tokenFromFile(tokenPath); err == nil {
		return config.Client(ctx, token), nil
	}

	authURL := config.AuthCodeURL("state-token", oauth2.AccessTypeOffline)
	fmt.Printf("Open the following link in your browser:\n%v\n", authURL)
	fmt.Printf("Paste the authorization code (or the full redirect URL) here:\n")

	var input string
	if _, err := fmt.Scan(&input); err != nil {
		return nil, fmt.Errorf("read auth input: %w", err)
	}

	code, err := extractAuthCode(input)
	if err != nil {
		return nil, err
	}

	tok, err := config.Exchange(ctx, code)
	if err != nil {
		return nil, fmt.Errorf("exchange token: %w", err)
	}

	if err := saveToken(tokenPath, tok); err != nil {
		return nil, err
	}

	return config.Client(ctx, tok), nil
}

func tokenFromFile(path string) (*oauth2.Token, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	tok := &oauth2.Token{}
	if err := json.NewDecoder(f).Decode(tok); err != nil {
		return nil, err
	}
	return tok, nil
}

func saveToken(path string, token *oauth2.Token) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("create token dir: %w", err)
	}

	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create token file: %w", err)
	}
	defer f.Close()

	if err := json.NewEncoder(f).Encode(token); err != nil {
		return fmt.Errorf("write token: %w", err)
	}
	return nil
}

func extractAuthCode(input string) (string, error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return "", fmt.Errorf("authorization code is empty")
	}
	if strings.HasPrefix(input, "http") {
		parsed, err := url.Parse(input)
		if err != nil {
			return "", fmt.Errorf("parse redirect url: %w", err)
		}
		code := parsed.Query().Get("code")
		if code == "" {
			return "", fmt.Errorf("redirect url missing code parameter")
		}
		return code, nil
	}
	return input, nil
}
