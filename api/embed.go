// Package api embeds the OpenAPI document and Swagger UI page.
package api

import "embed"

// Docs holds openapi.yaml, the Swagger UI page and its bundled assets.
//
//go:embed openapi.yaml swagger.html swagger/*
var Docs embed.FS
