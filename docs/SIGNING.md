# Code signing and notarization

The Release workflow (`.github/workflows/release.yml`) signs installers **only when the matching repo secrets exist**. With no secrets, releases build exactly as before: unsigned. You can add macOS, Windows, or both, in any order.

## What signing changes

- **macOS:** no Gatekeeper "damaged / can't be opened" warning, no `xattr -cr` workaround, and **auto-update starts working**. The signed build carries `"harnessSigned": true` in its `package.json`, which the app reads to turn on macOS auto-update. Unsigned builds never have it and keep the manual "Update vX available" button.
- **Windows:** fewer SmartScreen warnings. A trusted-signing or standard certificate builds reputation over time, so warnings may persist for the first releases.
- **Linux:** nothing changes (AppImage is not signed).

## What to obtain

### macOS

1. An [Apple Developer Program](https://developer.apple.com/programs/) membership ($99/year).
2. A **Developer ID Application** certificate (Xcode, Settings, Accounts, Manage Certificates; or developer.apple.com, Certificates). Export it from Keychain Access as a `.p12` with a password.
3. An **App Store Connect API key** for notarization (App Store Connect, Users and Access, Integrations, Team Keys; the Developer role is enough). Download the `.p8` once, and note the Key ID and the Issuer ID.
   - Alternative: an Apple ID with an app-specific password (appleid.apple.com) and your Team ID.

### Windows

Pick one:

- **(a) Azure Trusted Signing** (recommended, roughly $10/month). Create a Trusted Signing account and a certificate profile in the Azure portal. Identity validation is required and can take days. Create an app registration (service principal), give it the **Trusted Signing Certificate Profile Signer** role on the account, and create a client secret.
- **(b) A code signing certificate** as a `.pfx` with a password (from any CA).

## Encoding files for secrets

Secrets that hold files are base64 text.

```sh
# macOS
base64 -i cert.p12 | pbcopy
# Linux
base64 -w0 cert.p12
```

```powershell
# Windows PowerShell
[Convert]::ToBase64String([IO.File]::ReadAllBytes("cert.p12")) | Set-Clipboard
```

Use the same for the `.p8` and `.pfx` files. Paste the result as the secret value with no extra whitespace.

## Secrets and variables

Add these under the repo's **Settings, Secrets and variables, Actions** (Secrets tab; Variables tab for the last table).

### macOS secrets

| Secret | Value |
| --- | --- |
| `MAC_CSC_LINK` | base64 `.p12` Developer ID Application certificate. **Setting this turns macOS signing on.** |
| `MAC_CSC_KEY_PASSWORD` | the `.p12` password (required together with the above) |

Notarization, preferred option (App Store Connect API key):

| Secret | Value |
| --- | --- |
| `APPLE_API_KEY` | base64 content of the `.p8` file (the workflow writes it to a temp file and deletes it afterwards) |
| `APPLE_API_KEY_ID` | the key ID |
| `APPLE_API_ISSUER` | the issuer ID |

Notarization, alternative (used only if the API key set is incomplete):

| Secret | Value |
| --- | --- |
| `APPLE_ID` | Apple ID email |
| `APPLE_APP_SPECIFIC_PASSWORD` | app-specific password |
| `APPLE_TEAM_ID` | 10-character team ID |

If macOS signing is on but neither notarization set is complete, the app is signed but not notarized, and the build logs a warning. Gatekeeper will still complain for downloaded copies, so add notarization.

### Windows, option (a): Azure Trusted Signing

| Kind | Name | Value |
| --- | --- | --- |
| Secret | `AZURE_TENANT_ID` | Entra tenant ID |
| Secret | `AZURE_CLIENT_ID` | service principal application (client) ID |
| Secret | `AZURE_CLIENT_SECRET` | service principal secret |
| Variable | `AZURE_SIGNING_ENDPOINT` | regional endpoint of the account, e.g. `https://eus.codesigning.azure.net` |
| Variable | `AZURE_SIGNING_ACCOUNT` | Trusted Signing account name |
| Variable | `AZURE_SIGNING_PROFILE` | certificate profile name |
| Variable | `AZURE_SIGNING_PUBLISHER` | publisher name, exactly the certificate subject (CN) of the profile |

All seven must be set for Azure signing to turn on. These map to electron-builder's `win.azureSignOptions` (`endpoint`, `codeSigningAccountName`, `certificateProfileName`, `publisherName`).

### Windows, option (b): `.pfx`

| Secret | Value |
| --- | --- |
| `WIN_CSC_LINK` | base64 `.pfx` |
| `WIN_CSC_KEY_PASSWORD` | its password |

If both Azure and a `.pfx` are configured, **Azure wins**.

## Dry run (test signing without a release)

After adding secrets, run:

```sh
gh workflow run release.yml -f publish=false
```

(or Actions, Release, Run workflow, leaving "publish" unchecked). The run uses the branch you select. It builds all three OSes with the same signing logic as a real release, runs `electron-builder --publish never`, and uploads the installers as workflow artifacts (`installers-<os>`, kept 7 days). It does no tag checks, creates no release, and skips the publish job. The "Detect signing configuration" step log shows which modes were switched on.

Real releases are unchanged: pushing a `v*` tag still verifies the version, creates the draft, builds, signs, uploads and publishes.

To check a macOS artifact after downloading:

```sh
codesign --verify --deep --strict --verbose=2 "Claude Harness.app"
codesign -dv "Claude Harness.app/Contents/Resources/bin/harness-core"   # look for flags=0x10000(runtime)
spctl --assess --type execute -vv "Claude Harness.app"                   # "accepted, source=Notarized Developer ID"
xcrun stapler validate "Claude Harness.app"
grep harnessSigned "Claude Harness.app/Contents/Resources/app.asar"       # present when signed
```

## How it is wired (what was verified in electron-builder 26.18.1)

- `CSC_LINK` / `CSC_KEY_PASSWORD` carry the mac certificate; `CSC_IDENTITY_AUTO_DISCOVERY=false` stops keychain lookup and is kept for unsigned builds only.
- Notarization is controlled by `mac.notarize` (boolean) and these env vars (`app-builder-lib/out/mac/MacTargetHelper.js`, `getNotarizeOptions`): `APPLE_API_KEY` (a **path** to the `.p8`), `APPLE_API_KEY_ID`, `APPLE_API_ISSUER`; or `APPLE_ID`, `APPLE_APP_SPECIFIC_PASSWORD`, `APPLE_TEAM_ID`. If Apple ID variables are present electron-builder uses them first, so the workflow unsets them when the API key is used.
- Hardened runtime and the entitlements in `ui/build/entitlements.mac.plist` and `entitlements.mac.inherit.plist` (JIT, unsigned executable memory, disabled library validation) are set in `ui/electron-builder.yml`.
- **The bundled Go core is signed without extra config.** `@electron/osx-sign` walks every file under the app's `Contents` folder and signs each Mach-O binary (`dist/cjs/util.js` `walkAsync`, `dist/cjs/sign.js`), so `Contents/Resources/bin/harness-core` (the lipo universal binary) gets the same identity, hardened runtime and timestamp. `mac.binaries` is therefore not needed.
- Windows: `WIN_CSC_LINK`/`WIN_CSC_KEY_PASSWORD` are read in `winPackager.js`; Azure uses `win.azureSignOptions`, which requires `publisherName`, `endpoint`, `certificateProfileName` and `codeSigningAccountName` (`options/winOptions.d.ts`) and authenticates through the `AZURE_TENANT_ID`, `AZURE_CLIENT_ID`, `AZURE_CLIENT_SECRET` environment credentials. It installs the `TrustedSigning` PowerShell module on the runner.
- `harnessSigned` is injected with `-c.extraMetadata.harnessSigned=true`, only on the signed macOS path.

## Troubleshooting

- **Nothing was signed:** check the "Detect signing configuration" log line. A group turns on only when every secret in it is non-empty (Azure also needs the four variables). Secrets are not available to workflow runs from forks.
- **"skipped macOS application code signing" / no identity found:** `MAC_CSC_LINK` is not valid base64 of a `.p12` containing a *Developer ID Application* certificate (not "Apple Development" or an installer certificate), or the password is wrong.
- **Notarization fails with a 401 / invalid key:** `APPLE_API_KEY` must be the base64 of the `.p8` file contents, and the key ID and issuer must belong to the same team. The key needs the Developer role or higher.
- **Notarization rejected, "not signed with hardened runtime" or "binary not signed":** open the log link in the error output; check that `harness-core` shows in the signed list. Do not add `mac.binaries` unless it is missing.
- **App launches then quits only when signed:** a missing entitlement; the standard set is in `ui/build/*.plist`.
- **Windows: `Invoke-TrustedSigning` errors (403/401):** the service principal lacks the Certificate Profile Signer role, the endpoint region does not match the account, or the publisher name differs from the certificate subject.
- **SmartScreen still warns:** reputation builds with downloads, even with a valid signature.
- **Rotating a certificate:** replace the secret; nothing else changes. Users on older signed builds keep updating as long as the Team ID stays the same.
