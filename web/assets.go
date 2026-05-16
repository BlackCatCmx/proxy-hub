package web

import "embed"

//go:embed index.html login.html app.js app.css vendor/alpine.min.js
var FS embed.FS
