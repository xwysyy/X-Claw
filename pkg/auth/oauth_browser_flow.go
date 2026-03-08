package auth

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type callbackResult struct {
	code string
	err  error
}

func startOAuthCallbackServer(port int, state string, resultCh chan<- callbackResult) (*http.Server, error) {
	mux := http.NewServeMux()
	mux.HandleFunc("/auth/callback", newOAuthCallbackHandler(state, resultCh))

	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return nil, fmt.Errorf("starting callback server on port %d: %w", port, err)
	}

	server := &http.Server{Handler: mux}
	go func() {
		_ = server.Serve(listener)
	}()
	return server, nil
}

func shutdownOAuthCallbackServer(server *http.Server) {
	if server == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = server.Shutdown(ctx)
}

func newOAuthCallbackHandler(expectedState string, resultCh chan<- callbackResult) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("state") != expectedState {
			resultCh <- callbackResult{err: fmt.Errorf("state mismatch")}
			http.Error(w, "State mismatch", http.StatusBadRequest)
			return
		}

		code := r.URL.Query().Get("code")
		if code == "" {
			errMsg := r.URL.Query().Get("error")
			resultCh <- callbackResult{err: fmt.Errorf("no code received: %s", errMsg)}
			http.Error(w, "No authorization code received", http.StatusBadRequest)
			return
		}

		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, "<html><body><h2>Authentication successful!</h2><p>You can close this window.</p></body></html>")
		resultCh <- callbackResult{code: code}
	}
}

func printOAuthBrowserInstructions(authURL string, port int) {
	fmt.Printf("Open this URL to authenticate:\n\n%s\n\n", authURL)
	if err := OpenBrowser(authURL); err != nil {
		fmt.Printf("Could not open browser automatically.\nPlease open this URL manually:\n\n%s\n\n", authURL)
	}

	fmt.Printf("Wait! If you are in a headless environment (like Coolify/VPS) and cannot reach localhost:%d,\n", port)
	fmt.Println("please complete the login in your local browser and then PASTE the final redirect URL (or just the code) here.")
	fmt.Println("Waiting for authentication (browser or manual paste)...")
}

func startManualAuthInput(r io.Reader) <-chan string {
	manualCh := make(chan string, 1)
	go func() {
		reader := bufio.NewReader(r)
		input, _ := reader.ReadString('\n')
		manualCh <- strings.TrimSpace(input)
	}()
	return manualCh
}

func waitForAuthorizationCode(
	resultCh <-chan callbackResult,
	manualCh <-chan string,
	timeout <-chan time.Time,
) (string, error) {
	select {
	case result := <-resultCh:
		if result.err != nil {
			return "", result.err
		}
		return result.code, nil
	case manualInput := <-manualCh:
		if manualInput == "" {
			return "", fmt.Errorf("manual input canceled")
		}
		return extractAuthorizationCode(manualInput)
	case <-timeout:
		return "", fmt.Errorf("authentication timed out after 5 minutes")
	}
}

func extractAuthorizationCode(manualInput string) (string, error) {
	code := manualInput
	if strings.Contains(manualInput, "?") {
		u, err := url.Parse(manualInput)
		if err == nil {
			code = u.Query().Get("code")
		}
	}
	if code == "" {
		return "", fmt.Errorf("could not find authorization code in input")
	}
	return code, nil
}
