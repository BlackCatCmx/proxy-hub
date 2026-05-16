package web

import "embed"

//go:embed index.html login.html app.js app.css
var FS embed.FS
