// Package docs embeds the documents the client prints (hearth docs env).
package docs

import _ "embed"

// ClientEnvZH and ClientEnvEN are the client software setup guide.
//
//go:embed zh/client-env.md
var ClientEnvZH string

//go:embed en/client-env.md
var ClientEnvEN string
