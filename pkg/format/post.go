package format

import (
	"bytes"
	"cmp"
	"context"
	"fmt"
	"html/template"
	"slices"
	"strings"
	"time"

	"bsky.watch/utils/xrpcauth"
	"github.com/bluesky-social/indigo/api/bsky"
	"github.com/bluesky-social/indigo/xrpc"
	"github.com/mattn/go-redmine"
)

type Uploader interface {
	Upload(ctx context.Context, filename string, content []byte) (*redmine.Upload, error)
}

var postTemplate = template.Must(helperTemplates.New("post").Parse(`
| [Post by {{template "quoteTableCell" .Author}} @ {{.Timestamp}}]({{.URL | quoteTableCell}}) |
| ------- |
{{- if .Post.Text }}
| {{ range (.Post | formatText | lines) }}{{. | quoteTableCell}}<br/>{{end}} |
{{- end }}
{{- range .Images }}
| ![]({{. | quoteTableCell}}) |
{{- end }}
{{- with .Post.Langs }}
| Languages: {{ range .}}{{. | quoteTableCell}} {{end}}
{{- end }}
{{ with .Embeds }}
Embedded content:

{{ range . }}{{ .Render }}

{{ end }}
{{- end }}
`))

type postData struct {
	Post      *bsky.FeedPost
	URL       string
	Author    string
	Timestamp time.Time
	Images    []string
	Embeds    []embed
}

func PostFromCommit(ctx context.Context, post *bsky.FeedPost, uploader Uploader) (string, error) {
	return "", fmt.Errorf("not implemented")
}

func makePostData(ctx context.Context, client *xrpc.Client, post *bsky.FeedPost, author *bsky.ActorDefs_ProfileViewDetailed, rkey string, uploader Uploader) (*postData, error) {
	data := &postData{
		Post: post,
		URL:  fmt.Sprintf("https://bsky.app/profile/%s/post/%s", author.Did, rkey),
	}

	switch {
	case author.DisplayName != nil:
		data.Author = *author.DisplayName
	case author.Handle != "":
		data.Author = author.Handle
	default:
		data.Author = author.Did
	}

	t, err := time.Parse(time.RFC3339, post.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("parsing createdAt %q: %w", post.CreatedAt, err)
	}
	data.Timestamp = t

	if post.Embed != nil {
		switch {
		case post.Embed.EmbedImages != nil:
			for _, image := range post.Embed.EmbedImages.Images {
				if image.Image == nil {
					continue
				}

				href, err := uploadBlob(ctx, client, uploader, image.Image, author.Did)
				if err != nil {
					return nil, err
				}

				data.Images = append(data.Images, href)
			}
		case post.Embed.EmbedExternal != nil && post.Embed.EmbedExternal.External != nil:
			embed, err := makeLinkEmbed(ctx, client, uploader, post.Embed.EmbedExternal.External, author.Did)
			if err != nil {
				return nil, err
			}
			data.Embeds = append(data.Embeds, embed)
		case post.Embed.EmbedRecord != nil && post.Embed.EmbedRecord.Record != nil:
			embed, err := makeRecordEmbed(ctx, client, post.Embed.EmbedRecord.Record, uploader)
			if err != nil {
				return nil, err
			}
			data.Embeds = append(data.Embeds, embed)
		case post.Embed.EmbedRecordWithMedia != nil:
			if post.Embed.EmbedRecordWithMedia.Record != nil {
				embed, err := makeRecordEmbed(ctx, client, post.Embed.EmbedRecordWithMedia.Record.Record, uploader)
				if err != nil {
					return nil, err
				}
				data.Embeds = append(data.Embeds, embed)
			}
			if post.Embed.EmbedRecordWithMedia.Media != nil {
				media := post.Embed.EmbedRecordWithMedia.Media
				switch {
				case media.EmbedImages != nil:
					for _, image := range media.EmbedImages.Images {
						if image.Image == nil {
							continue
						}

						href, err := uploadBlob(ctx, client, uploader, image.Image, author.Did)
						if err != nil {
							return nil, err
						}

						data.Images = append(data.Images, href)
					}
				case media.EmbedExternal != nil:
					embed, err := makeLinkEmbed(ctx, client, uploader, media.EmbedExternal.External, author.Did)
					if err != nil {
						return nil, err
					}
					data.Embeds = append(data.Embeds, embed)
				}
			}
		}
	}
	return data, nil
}

func Post(ctx context.Context, client *xrpc.Client, post *bsky.FeedPost, author *bsky.ActorDefs_ProfileViewDetailed, rkey string, uploader Uploader) (string, error) {
	c := xrpcauth.NewAnonymousClient(ctx)
	c.Host = "https://api.bsky.app"
	c.Client = client.Client
	client = c

	data, err := makePostData(ctx, client, post, author, rkey, uploader)
	if err != nil {
		return "", err
	}

	w := bytes.NewBuffer(nil)
	if err := postTemplate.Execute(w, data); err != nil {
		return "", err
	}
	return w.String(), nil
}

func formatText(post *bsky.FeedPost) string {
	if len(post.Facets) == 0 {
		// Shortcut.
		return post.Text
	}

	// XXX: Even though strings should be UTF-8-encoded, offsets are defined on bytes,
	// which means that a facet can potentially cut a UTF-8 sequence in half, resulting
	// in an invalid encoding.
	var r bytes.Buffer
	ss := []byte(post.Text)
	prev := int64(0)
	facets := slices.Clone(post.Facets)
	slices.SortFunc(facets, func(a, b *bsky.RichtextFacet) int { return cmp.Compare(a.Index.ByteStart, b.Index.ByteStart) })
	for _, facet := range facets {
		if facet.Index.ByteStart < prev {
			// Either a duplicate or some bug
			continue
		}
		r.Write(ss[prev:facet.Index.ByteStart])
		switch {
		case len(facet.Features) != 1:
			// No idea what to do in such cases
			r.Write(ss[facet.Index.ByteStart:facet.Index.ByteEnd])
		case facet.Features[0].RichtextFacet_Link != nil:
			r.WriteString(fmt.Sprintf("[%s](%s)",
				string(ss[facet.Index.ByteStart:facet.Index.ByteEnd]),
				facet.Features[0].RichtextFacet_Link.Uri))
		case facet.Features[0].RichtextFacet_Mention != nil:
			r.WriteString(fmt.Sprintf("[%s](https://bsky.app/profile/%s)",
				string(ss[facet.Index.ByteStart:facet.Index.ByteEnd]),
				facet.Features[0].RichtextFacet_Mention.Did))
		case facet.Features[0].RichtextFacet_Tag != nil:
			tag := facet.Features[0].RichtextFacet_Tag.Tag
			if !strings.HasPrefix(tag, "#") {
				tag = "#" + tag
			}
			r.WriteString(fmt.Sprintf("%s(`%s`)",
				string(ss[facet.Index.ByteStart:facet.Index.ByteEnd]),
				tag))
		default:
			r.Write(ss[facet.Index.ByteStart:facet.Index.ByteEnd])
		}
		prev = facet.Index.ByteEnd
	}
	r.Write(ss[prev:])
	return r.String()
}
