# mdtogdoc

Converts a Markdown file to a Google Doc and returns the URL.

```
mdtogdoc [-title TITLE] [FILE]
```

Reads from stdin if no `FILE` is given. The document is created in the authenticated user's Google Drive.

## Install

```sh
go install
```

The binary lands in `~/go/bin`. Make sure that's in your `$PATH`.

## Google Cloud setup

You need an OAuth 2.0 **Desktop app** credential from Google Cloud. Do this once.

1. Go to [Google Cloud Console → APIs & Services → Credentials](https://console.cloud.google.com/apis/credentials).
2. If you don't have a project yet, create one (name doesn't matter).
3. Click **Enable APIs and Services**, search for **Google Drive API**, and enable it.
4. Back on the Credentials page, click **Create Credentials → OAuth client ID**.
5. Set the application type to **Desktop app**. Give it any name.
6. Click **Create**, then **Download JSON**. Save the file as `credentials.json`.

> The credential type must be **Desktop app**. Web application credentials use a different OAuth flow and won't work.

## Authentication

Put `credentials.json` in `~/.config/mdtogdoc/` and run:

```sh
mdtogdoc -setup
```

This opens your browser, asks you to approve Drive access, then saves a `token.json` to `~/.config/mdtogdoc/`. You only need to do this once. The token refreshes automatically.

```sh
mkdir -p ~/.config/mdtogdoc
mv credentials.json ~/.config/mdtogdoc/
mdtogdoc -setup
```

## Usage

```sh
# From a file
mdtogdoc report.md

# With a custom title
mdtogdoc -title "Q2 Report" report.md

# From stdin
cat notes.md | mdtogdoc -title "Notes"
```

Prints the Google Docs URL on success:

```
https://docs.google.com/document/d/1abc.../edit
```

