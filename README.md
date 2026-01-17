# Auto Key Presser (Windows + macOS)

Small UI app that presses selected keys indefinitely at a given interval.

## Features
- Add multiple keys with different intervals
- Start/stop all keys at once
- Simple Windows UI

## Supported keys
- Letters: `A`-`Z`
- Digits: `0`-`9`
- Function keys: `F1`-`F12`
- Special: `SPACE`, `ENTER`, `ESC`, `TAB`, `UP`, `DOWN`, `LEFT`, `RIGHT`

## Build (Windows)
```
go mod tidy
go build
```

Run `autokeypress.exe`.

## Build (macOS)
```
go mod tidy
go build
```

Run `./autokeypress`.

If the macOS build fails, install Xcode Command Line Tools:
```
xcode-select --install
```

## Build per platform (from any OS)
macOS (Apple Silicon):
```
GOOS=darwin GOARCH=arm64 go build -o autokeypress-mac
```

macOS (Intel):
```
GOOS=darwin GOARCH=amd64 go build -o autokeypress-mac
```

Windows:
```
GOOS=windows GOARCH=amd64 go build -o autokeypress-win.exe
```

Note: macOS builds require CGO and Apple frameworks, so cross-compiling
from non-macOS hosts may fail. Build macOS on macOS if needed.
