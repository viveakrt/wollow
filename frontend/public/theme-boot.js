// Stamps the saved theme on <html> before first paint, so an explicit dark
// choice doesn't flash the light palette on load.
//
// This lives in its own file rather than inline in index.html because the app's
// Content-Security-Policy is `script-src 'self'` — an inline script would be
// blocked, and the flash it exists to prevent would come back. It is loaded
// render-blocking (no `defer`, no `type=module`) because running after first
// paint would defeat the point.
try {
  var saved = localStorage.getItem('wollow.theme')
  if (saved === 'light' || saved === 'dark') {
    document.documentElement.setAttribute('data-theme', saved)
  }
} catch {
  // Private browsing or blocked storage: the system preference still applies.
}
