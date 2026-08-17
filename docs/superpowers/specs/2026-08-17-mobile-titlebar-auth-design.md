# Mobile Titlebar and API Token Design

## Goal

Keep the phone UI's current one-window fullscreen model, but make each mobile window's titlebar more useful and make API-token auth clear when the browser has no saved token.

## Requirements

- Keep mobile windows fullscreen by default. Opening a window still swaps it into the single visible app slot and preserves the mobile back stack.
- Reuse the existing desktop titlebar controls on mobile where they make sense:
  - close closes the current window;
  - minimize collapses the window into the existing visible bottom titlebar;
  - zoom becomes a mobile restore/fullscreen toggle for windows that should not always consume the whole phone viewport.
- Do not bring desktop drag or grow-box resizing into mobile mode.
- Keep the desktop layout snapshot isolated from mobile state.
- When an API call returns `401`, open the existing API Token window automatically, focus its password field, and show a concise message that the daemon requires `Authorization: Bearer <token>`.
- Continue storing the browser-entered token only in `localStorage` under `exe_token`; the daemon's required token remains in `config.json` or `EXE_API_TOKEN`.
- Never print or expose the token value in UI text, logs, docs, tests, or screenshots.

## Approach

The implementation stays in `internal/server/ui/index.html`. Mobile CSS will keep the fullscreen layout as the default, but stop hiding the relevant titlebar control for windows that opt into mobile restore. The window manager will add a small mobile-only mode flag on each window: fullscreen by default, restored when the titlebar zoom button is tapped, then fullscreen again when tapped a second time. Restored mobile windows remain fixed, centered, viewport-clamped, and non-draggable.

The API helper will treat `401` specially. If the request was not already saving the token, it will open `#win-token`, place a short status line in that dialog, and keep the failed operation from repeatedly spawning dialogs. After the user saves the token, normal API calls include the existing Bearer header path.

## Testing

- Add static UI tests for mobile titlebar controls staying available and desktop drag/grow behavior staying disabled on mobile.
- Add static UI tests for the `401` path opening the API Token window and for the token continuing to use `localStorage` key `exe_token`.
- Run the Go test suite after implementation.

## Out of scope

- Replacing Bearer-token auth with OAuth or sessions.
- Sharing tokens between browsers or devices.
- Letting phone users freely drag or resize spatial desktop windows.
