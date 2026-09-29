//go:build tools
// +build tools

package main

import (
	_ "github.com/jackc/pgx/v5"
	_ "github.com/zitadel/oidc/v3"
)
