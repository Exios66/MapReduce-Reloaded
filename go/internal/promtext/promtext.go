// Package promtext writes the Prometheus text exposition format, version
// 0.0.4, for the coordinator's and workers' /metrics.
package promtext

import (
	"bytes"
	"fmt"
	"strings"
)

// ContentType is the Content-Type of a /metrics response.
const ContentType = "text/plain; version=0.0.4; charset=utf-8"

// Family writes the HELP and TYPE lines that precede a metric family's
// samples; typ is "counter" or "gauge".
func Family(b *bytes.Buffer, name, typ, help string) {
	fmt.Fprintf(b, "# HELP %s %s\n# TYPE %s %s\n", name, help, name, typ)
}

var escape = strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`)

// Label quotes v as a label value.
func Label(v string) string { return `"` + escape.Replace(v) + `"` }
