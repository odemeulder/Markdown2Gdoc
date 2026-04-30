package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/drive/v3"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"
)

const scope = "https://www.googleapis.com/auth/drive.file"

func findFile(envVar, xdgName, localName string) string {
	if v := os.Getenv(envVar); v != "" {
		return v
	}
	home, err := os.UserHomeDir()
	if err == nil {
		xdg := filepath.Join(home, ".config", "mdtogdoc", xdgName)
		if _, err := os.Stat(xdg); err == nil {
			return xdg
		}
	}
	return localName
}

func loadOAuthConfig() (*oauth2.Config, error) {
	credFile := findFile("GOOGLE_CREDENTIALS_FILE", "credentials.json", "credentials.json")
	data, err := os.ReadFile(credFile)
	if err != nil {
		return nil, fmt.Errorf("cannot read credentials file %q: %w\nGet it from https://console.cloud.google.com/ (OAuth 2.0 Desktop app)", credFile, err)
	}
	cfg, err := google.ConfigFromJSON(data, scope)
	if err != nil {
		return nil, fmt.Errorf("invalid credentials file: %w", err)
	}
	return cfg, nil
}

func tokenPath() string {
	return findFile("GOOGLE_TOKEN_FILE", "token.json", "token.json")
}

func loadToken() (*oauth2.Token, error) {
	data, err := os.ReadFile(tokenPath())
	if err != nil {
		return nil, err
	}
	var tok oauth2.Token
	if err := json.Unmarshal(data, &tok); err != nil {
		return nil, err
	}
	return &tok, nil
}

func saveToken(tok *oauth2.Token) error {
	path := tokenPath()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	data, err := json.Marshal(tok)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}

func getHTTPClient(ctx context.Context, cfg *oauth2.Config) (*http.Client, error) {
	tok, err := loadToken()
	if err != nil {
		return nil, fmt.Errorf("not authenticated — run: mdtogdoc -setup")
	}
	ts := cfg.TokenSource(ctx, tok)
	newTok, err := ts.Token()
	if err != nil {
		return nil, fmt.Errorf("token refresh failed — run: mdtogdoc -setup")
	}
	if newTok.AccessToken != tok.AccessToken {
		_ = saveToken(newTok)
	}
	return oauth2.NewClient(ctx, ts), nil
}

func runSetup(ctx context.Context, cfg *oauth2.Config) error {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("starting local server: %w", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port

	cfg.RedirectURL = fmt.Sprintf("http://localhost:%d", port)

	codeCh := make(chan string, 1)
	errCh := make(chan error, 1)

	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		code := r.URL.Query().Get("code")
		if code == "" {
			errCh <- fmt.Errorf("no code in callback: %s", r.URL.RawQuery)
			http.Error(w, "Authentication failed.", http.StatusBadRequest)
			return
		}
		fmt.Fprintln(w, "<html><body><h2>Authentication successful. You may close this tab.</h2></body></html>")
		codeCh <- code
	})}

	go func() {
		if err := srv.Serve(listener); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()
	defer srv.Shutdown(ctx) //nolint:errcheck

	authURL := cfg.AuthCodeURL("state", oauth2.AccessTypeOffline)
	fmt.Println("Opening browser for Google authentication...")
	openBrowser(authURL)
	fmt.Printf("\nIf the browser did not open, visit:\n%s\n\n", authURL)

	var code string
	select {
	case code = <-codeCh:
	case err = <-errCh:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}

	tok, err := cfg.Exchange(ctx, code)
	if err != nil {
		return fmt.Errorf("token exchange failed: %w", err)
	}
	if err := saveToken(tok); err != nil {
		return fmt.Errorf("saving token: %w", err)
	}
	fmt.Printf("Authentication successful. Token saved to %s\n", tokenPath())
	return nil
}

func openBrowser(url string) {
	var cmd string
	var args []string
	switch runtime.GOOS {
	case "darwin":
		cmd, args = "open", []string{url}
	case "windows":
		cmd, args = "rundll32", []string{"url.dll,FileProtocolHandler", url}
	default:
		cmd, args = "xdg-open", []string{url}
	}
	_ = exec.Command(cmd, args...).Start()
}

func convertMarkdown(src []byte) (string, error) {
	md := goldmark.New(
		goldmark.WithExtensions(
			extension.GFM,
			extension.Linkify,
		),
	)
	var buf bytes.Buffer
	if err := md.Convert(src, &buf); err != nil {
		return "", err
	}
	return "<html><body>" + buf.String() + "</body></html>", nil
}

func uploadToDrive(ctx context.Context, client *http.Client, title, mimeType, contentType string, body io.Reader) (string, error) {
	svc, err := drive.NewService(ctx, option.WithHTTPClient(client))
	if err != nil {
		return "", fmt.Errorf("creating drive service: %w", err)
	}
	meta := &drive.File{
		Name:     title,
		MimeType: mimeType,
	}
	file, err := svc.Files.Create(meta).
		Media(body, googleapi.ContentType(contentType)).
		Fields("id").
		Do()
	if err != nil {
		return "", fmt.Errorf("uploading to Drive: %w", err)
	}
	return file.Id, nil
}

func convertToSlides(mdPath string) ([]byte, error) {
	tmp, err := os.CreateTemp("", "mdtogdoc-*.pptx")
	if err != nil {
		return nil, err
	}
	tmp.Close()
	defer os.Remove(tmp.Name())

	cmd := exec.Command("marp", "--pptx", "--output", tmp.Name(), mdPath)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("marp failed (is marp installed? https://marp.app): %w", err)
	}
	return os.ReadFile(tmp.Name())
}

func main() {
	setup := flag.Bool("setup", false, "run OAuth flow to authenticate")
	title := flag.String("title", "", "document title (default: filename stem)")
	slides := flag.Bool("slides", false, "create a Google Slides presentation (requires marp in PATH)")
	flag.Parse()

	ctx := context.Background()

	cfg, err := loadOAuthConfig()
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}

	if *setup {
		if err := runSetup(ctx, cfg); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		return
	}

	var src []byte
	var docTitle string
	var inputFilePath string
	readFromStdin := false

	args := flag.Args()
	switch len(args) {
	case 0:
		src, err = io.ReadAll(os.Stdin)
		if err != nil {
			fmt.Fprintln(os.Stderr, "error reading stdin:", err)
			os.Exit(1)
		}
		docTitle = "Untitled Document"
		readFromStdin = true
	case 1:
		inputFilePath = args[0]
		src, err = os.ReadFile(inputFilePath)
		if err != nil {
			fmt.Fprintln(os.Stderr, "error reading file:", err)
			os.Exit(1)
		}
		stem := strings.TrimSuffix(filepath.Base(inputFilePath), filepath.Ext(inputFilePath))
		docTitle = stem
	default:
		fmt.Fprintln(os.Stderr, "usage: mdtogdoc [-title TITLE] [-slides] [-setup] [FILE]")
		os.Exit(1)
	}

	if *title != "" {
		docTitle = *title
	}

	client, err := getHTTPClient(ctx, cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}

	if *slides {
		mdPath := inputFilePath
		if readFromStdin {
			f, err := os.CreateTemp("", "mdtogdoc-*.md")
			if err != nil {
				fmt.Fprintln(os.Stderr, "error creating temp file:", err)
				os.Exit(1)
			}
			if _, err := f.Write(src); err != nil {
				fmt.Fprintln(os.Stderr, "error writing temp file:", err)
				os.Exit(1)
			}
			f.Close()
			defer os.Remove(f.Name())
			mdPath = f.Name()
		}
		pptx, err := convertToSlides(mdPath)
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		id, err := uploadToDrive(ctx, client, docTitle,
			"application/vnd.google-apps.presentation",
			"application/vnd.openxmlformats-officedocument.presentationml.presentation",
			bytes.NewReader(pptx))
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		fmt.Printf("https://docs.google.com/presentation/d/%s/edit\n", id)
	} else {
		html, err := convertMarkdown(src)
		if err != nil {
			fmt.Fprintln(os.Stderr, "error converting markdown:", err)
			os.Exit(1)
		}
		id, err := uploadToDrive(ctx, client, docTitle,
			"application/vnd.google-apps.document",
			"text/html",
			strings.NewReader(html))
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		fmt.Printf("https://docs.google.com/document/d/%s/edit\n", id)
	}
}
