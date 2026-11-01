// Loaded as a blocking script so the page doesn't flash the wrong theme. Kept
// out of index.html because the admin CSP disallows inline scripts.
try {
  var saved = localStorage.getItem("otlp-router:theme")
  var dark = saved ? saved === "dark" : matchMedia("(prefers-color-scheme: dark)").matches
  document.documentElement.classList.toggle("dark", dark)
} catch (e) {}
