package format

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"strings"

	"bsky.watch/utils/aturl"
	comatproto "github.com/bluesky-social/indigo/api/atproto"
	"github.com/bluesky-social/indigo/api/bsky"
	"github.com/bluesky-social/indigo/xrpc"
)

type embed interface {
	Render() (template.HTML, error)
}

type linkEmbed struct {
	Title       string
	Uri         string
	Description string
	Thumb       string
}

var linkTemplate = template.Must(helperTemplates.New("linkEmbed").Parse(`
| Web link: <a href="{{.Uri}}">{{.Uri | quoteTableCell}}</a> |
| --------- |
{{- with .Title}}
| Title: {{. | quoteTableCell}} |
{{- end }}
{{- with .Thumb }}
| ![]({{.}}) |
{{- end }}
{{- with .Description }}
| Description:<br/>{{range (. | lines)}}{{. | quoteTableCell}}<br/>{{end}} |
{{- end }}
`))

func (e *linkEmbed) Render() (template.HTML, error) {
	w := bytes.NewBuffer(nil)
	if err := linkTemplate.Execute(w, e); err != nil {
		return "", fmt.Errorf("executing link embed template: %w", err)
	}
	return template.HTML(w.String()), nil
}

func makeLinkEmbed(ctx context.Context, client *xrpc.Client, uploader Uploader, embed *bsky.EmbedExternal_External, authorDid string) (*linkEmbed, error) {
	l := &linkEmbed{
		Title:       embed.Title,
		Uri:         embed.Uri,
		Description: embed.Description,
	}
	if embed.Thumb != nil {
		href, err := uploadBlob(ctx, client, uploader, embed.Thumb, authorDid)
		if err != nil {
			return nil, err
		}
		l.Thumb = href
	}
	return l, nil
}

type genericRecordEmbed struct {
	Uri  string
	JSON template.JS
}

var genericRecordTemplate = template.Must(helperTemplates.New("recordEmbed").Parse(`
| URI: ` + "`{{.Uri | quoteTableCell}}`" + ` |
| --------- |

<pre lang="json">
{{.JSON}}
</pre>`))

func (e *genericRecordEmbed) Render() (template.HTML, error) {
	w := bytes.NewBuffer(nil)
	if err := genericRecordTemplate.Execute(w, e); err != nil {
		return "", err
	}
	return template.HTML(w.String()), nil
}

type postEmbed struct {
	data *postData
}

func (e *postEmbed) Render() (template.HTML, error) {
	w := bytes.NewBuffer(nil)
	if err := postTemplate.Execute(w, e.data); err != nil {
		return "", err
	}
	return template.HTML(w.String()), nil
}

func makeRecordEmbed(ctx context.Context, client *xrpc.Client, ref *comatproto.RepoStrongRef, uploader Uploader) (embed, error) {
	u, err := aturl.Parse(ref.Uri)
	if err != nil {
		return nil, fmt.Errorf("parsing URI: %w", err)
	}
	parts := strings.Split(strings.TrimPrefix(u.Path, "/"), "/")
	if len(parts) < 2 {
		return nil, fmt.Errorf("not enough path components: %q", ref.Uri)
	}

	// TODO: figure out proper request routing to make fetching with CID work.
	resp, err := comatproto.RepoGetRecord(ctx, client, "" /*ref.Cid*/, parts[0], u.Host, parts[1])
	if err != nil {
		return nil, fmt.Errorf("RepoGetRecord(%q): %w", ref.Uri, err)
	}

	switch parts[0] {
	case "app.bsky.feed.post":
		post, ok := resp.Value.Val.(*bsky.FeedPost)
		if !ok {
			return nil, fmt.Errorf("unexpected type for the post record: %T", resp.Value.Val)
		}

		author, err := bsky.ActorGetProfile(ctx, client, u.Host)
		if err != nil {
			return nil, fmt.Errorf("ActorGetProfile(%q): %w", u.Host, err)
		}

		data, err := makePostData(ctx, client, post, author, parts[1], uploader)
		if err != nil {
			return nil, err
		}
		data.Embeds = nil

		return &postEmbed{data: data}, nil
	default:
		b, err := json.MarshalIndent(resp.Value, "", "  ")
		if err != nil {
			return nil, fmt.Errorf("marshaling record %q: %w", ref.Uri, err)
		}
		return &genericRecordEmbed{Uri: ref.Uri, JSON: template.JS(b)}, nil
	}
}
