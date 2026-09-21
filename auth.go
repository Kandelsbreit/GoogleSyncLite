package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/drive/v3"
	"google.golang.org/api/option"
)

const (
	CredentialsFile = "credentials.json"
	TokenFile       = "token.json"
	RedirectPort    = 8085
)

type ClientSecretFile struct {
	Installed struct {
		ClientID     string   `json:"client_id"`
		ClientSecret string   `json:"client_secret"`
		RedirectURIs []string `json:"redirect_uris"`
		AuthURI      string   `json:"auth_uri"`
		TokenURI     string   `json:"token_uri"`
	} `json:"installed"`
}

func GetOAuthConfig() (*oauth2.Config, error) {
	data, err := os.ReadFile(CredentialsFile)
	if err != nil {
		return nil, fmt.Errorf("файл '%s' не найден. Пожалуйста, поместите credentials.json из Google Cloud в папку программы", CredentialsFile)
	}

	config, err := google.ConfigFromJSON(data, drive.DriveScope)
	if err != nil {
		return nil, fmt.Errorf("ошибка парсинга credentials.json: %w", err)
	}

	config.RedirectURL = fmt.Sprintf("http://localhost:%d/oauth2callback", RedirectPort)
	return config, nil
}

func LoadSavedToken() (*oauth2.Token, error) {
	f, err := os.Open(TokenFile)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	tok := &oauth2.Token{}
	err = json.NewDecoder(f).Decode(tok)
	return tok, err
}

func SaveToken(tok *oauth2.Token) error {
	f, err := os.OpenFile(TokenFile, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	return json.NewEncoder(f).Encode(tok)
}

func OpenBrowser(targetURL string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		// cmd.exe /c start interprets '&' as command separator!
		// In Windows start syntax: start "" "url" or rundll32 url.dll,FileProtocolHandler "url"
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", targetURL)
	case "darwin":
		cmd = exec.Command("open", targetURL)
	default:
		cmd = exec.Command("xdg-open", targetURL)
	}
	return cmd.Start()
}

func AuthenticateViaBrowser() (*oauth2.Token, error) {
	config, err := GetOAuthConfig()
	if err != nil {
		return nil, err
	}

	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", RedirectPort))
	if err != nil {
		return nil, fmt.Errorf("порт %d занят другой копией программы: %w", RedirectPort, err)
	}
	defer listener.Close()

	codeChan := make(chan string)
	errChan := make(chan error)

	// Generate cryptographically secure state token to prevent CSRF / code injection
	stateBytes := make([]byte, 24)
	if _, err := rand.Read(stateBytes); err != nil {
		return nil, fmt.Errorf("ошибка генерации state токена: %w", err)
	}
	expectedState := hex.EncodeToString(stateBytes)

	mux := http.NewServeMux()
	mux.HandleFunc("/oauth2callback", func(w http.ResponseWriter, r *http.Request) {
		incomingState := r.URL.Query().Get("state")
		if incomingState != expectedState {
			http.Error(w, "Недопустимый токен состояния (CSRF protection)", http.StatusForbidden)
			errChan <- errors.New("invalid oauth state token (possible CSRF attack)")
			return
		}

		code := r.URL.Query().Get("code")
		if code == "" {
			http.Error(w, "Код авторизации не получен", http.StatusBadRequest)
			errChan <- errors.New("auth code missing")
			return
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		io.WriteString(w, `
			<html>
			<body style="font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif; background: #16171a; color: #e1e3e6; display: flex; align-items: center; justify-content: center; height: 100vh; margin: 0;">
				<div style="background: #202227; padding: 40px; border-radius: 12px; text-align: center; border: 1px solid #2d3139; max-width: 450px;">
					<h2 style="color: #4ade80; margin-top: 0;">✓ Авторизация успешна!</h2>
					<p style="color: #9aa0a6;">Вы успешно вошли в Google Drive. Теперь вы можете закрыть эту вкладку браузера и вернуться в приложение.</p>
				</div>
			</body>
			</html>
		`)

		codeChan <- code
	})

	server := &http.Server{Handler: mux}
	go func() {
		_ = server.Serve(listener)
	}()

	authURL := config.AuthCodeURL(expectedState, oauth2.AccessTypeOffline, oauth2.ApprovalForce)
	_ = OpenBrowser(authURL)

	select {
	case code := <-codeChan:
		_ = server.Shutdown(context.Background())
		tok, err := config.Exchange(context.Background(), code)
		if err != nil {
			return nil, fmt.Errorf("ошибка обмена токена: %w", err)
		}
		_ = SaveToken(tok)
		return tok, nil
	case err := <-errChan:
		_ = server.Shutdown(context.Background())
		return nil, err
	case <-time.After(3 * time.Minute):
		_ = server.Shutdown(context.Background())
		return nil, errors.New("время ожидания авторизации в браузере истекло (таймаут 3 мин)")
	}
}

func GetDriveService(ctx context.Context) (*drive.Service, error) {
	config, err := GetOAuthConfig()
	if err != nil {
		return nil, err
	}

	tok, err := LoadSavedToken()
	if err != nil {
		return nil, errors.New("требуется авторизация")
	}

	tokenSource := config.TokenSource(ctx, tok)
	// Refresh check & save back if changed
	newTok, err := tokenSource.Token()
	if err == nil && newTok.AccessToken != tok.AccessToken {
		_ = SaveToken(newTok)
	}

	return drive.NewService(ctx, option.WithTokenSource(tokenSource))
}

func IsAuthenticated() bool {
	_, err := LoadSavedToken()
	return err == nil
}
