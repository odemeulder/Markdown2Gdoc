# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Build & run

```sh
go build ./...          # compile
go install              # install binary to ~/go/bin
./mdtogdoc -setup       # one-time OAuth flow
./mdtogdoc -title 'X' file.md
```

## Architecture

Single-file Go CLI (`main.go`). The flow is:

1. `loadOAuthConfig` reads `credentials.json` (Desktop app OAuth2 credential)
2. `getHTTPClient` loads `token.json` and refreshes it if needed
3. `convertMarkdown` renders Markdown → HTML via goldmark (GFM + Linkify)
4. `uploadToDrive` POSTs the HTML to the Drive API with MIME type `application/vnd.google-apps.document`, which triggers Google's automatic HTML→Docs conversion

`-setup` runs `runSetup`: spins up a local HTTP server on a random port, opens the browser for the OAuth consent screen, catches the auth code from the loopback callback, and saves the token.

Config files are found via `findFile`, which checks `$GOOGLE_CREDENTIALS_FILE` / `$GOOGLE_TOKEN_FILE` env vars, then `~/.config/mdtogdoc/`, then the local directory.
