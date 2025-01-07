package format

import (
	"context"
	"fmt"
	"html/template"
	"mime"
	"strings"

	comatproto "github.com/bluesky-social/indigo/api/atproto"
	"github.com/bluesky-social/indigo/lex/util"
	"github.com/bluesky-social/indigo/xrpc"
)

var templateFuncs = template.FuncMap{
	"blockquote": func(s string) string {
		lines := strings.Split(s, "\n")
		for i, l := range lines {
			lines[i] = "> " + l
		}
		return strings.Join(lines, "\n")
	},
	"lines": func(s string) []string {
		return strings.Split(s, "\n")
	},
	"formatText": formatText,
	"percentage": func(n int, total int) float64 {
		return 100 * float64(n) / float64(total)
	},
	"quoteTableCell": func(s string) string {
		return strings.ReplaceAll(s, "|", "\\|")
	},
	"backslash": func(badChar string, s string) string {
		return strings.ReplaceAll(s, badChar, "\\"+badChar)
	},
	"unsafe": func(s string) template.HTML {
		return template.HTML(s)
	},
}

var helperTemplates = template.Must(template.New("quoteTableCell").Funcs(templateFuncs).Parse(`
{{- $l := (. | backslash "|" | lines) }}
{{- index $l 0 }}
{{- range slice $l 1}}<br/>{{.}}{{ end -}}
`))

func uploadBlob(ctx context.Context, client *xrpc.Client, uploader Uploader, blob *util.LexBlob, did string) (string, error) {
	host := client.Host
	client.Host = "https://bsky.social"
	b, err := comatproto.SyncGetBlob(ctx, client, blob.Ref.String(), did)
	client.Host = host
	if err != nil {
		return "", fmt.Errorf("fetching image %q: %w", blob.Ref, err)
	}

	ext := ".png"
	if exts, err := mime.ExtensionsByType(blob.MimeType); err == nil && len(exts) > 0 {
		ext = exts[0]
	}
	filename := fmt.Sprintf("%s%s", blob.Ref, ext)
	attachment, err := uploader.Upload(ctx, filename, b)
	if err != nil {
		return "", fmt.Errorf("uploading image %q: %w", blob.Ref, err)
	}
	return fmt.Sprintf("/attachments/download/%d/%s", attachment.Id, filename), nil
}
