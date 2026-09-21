# @garcke/miyoushe-cli

Cross-platform installer for the native `miyoushe-cli` (`mys`) binary.

```bash
npx @garcke/miyoushe-cli@latest install
```

The installer:

- selects the GitHub Release binary for Windows, macOS, or Linux;
- supports x64 and arm64;
- verifies the binary against the release `checksums.txt` SHA-256 value;
- validates `mys --version` before replacing an existing installation;
- installs without administrator privileges.

The default destination is `%LOCALAPPDATA%\miyoushe-cli\bin` on Windows and
`~/.local/bin` on macOS/Linux. Override it when needed:

```bash
npx @garcke/miyoushe-cli@latest install --install-dir /custom/bin
```

Source and issues: <https://github.com/Garcke/miyoushe-cli>
