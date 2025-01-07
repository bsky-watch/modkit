package format

import (
	"bytes"
	"context"
	"fmt"
	"html/template"
	"io"
	"net/http"

	"github.com/bluesky-social/indigo/api/bsky"
	"github.com/rs/zerolog"
)

var profileTemplate = template.Must(helperTemplates.New("profile").Parse(
	`## {{ with .DisplayName }}{{.}}{{else}}{{with .Handle}}{{.}}{{end}}{{end}}

[{{ .Handle | backslash "]" | backslash "[" }}](https://bsky.app/profile/{{ .Did }})

{{ with .Description }}Bio:

{{ range (. | lines)}}> {{.}}
{{ end }}

{{end}}{{ with .Userpic }}Userpic:

![]({{.}})
{{ end }}{{ with .Banner }}
Profile banner:

![]({{.}})
{{ end }}
{{ with .Labels }}
Labels: {{ range . }}{{.Val}} {{ end }}
{{ end }}
<figure class="table">
<table style="width:auto;">
<tbody>
{{ with .PostsCount }}
<tr>
	<th>
		<p>Posts</p>
	</th>
	<td>
		<p>{{.}}</p>
	</td>
</tr>
{{ end }}
{{ with .FollowersCount }}
<tr>
	<th>
		<p>Followers</p>
	</th>
	<td>
		<p>{{.}}</p>
	</td>
</tr>
{{ end }}
{{ with .FollowsCount }}
<tr>
	<th>
		<p>Follows</p>
	</th>
	<td>
		<p>{{.}}</p>
	</td>
</tr>
{{ end }}
</tbody>
</table>
</figure>`))

type profileData struct {
	*bsky.ActorDefs_ProfileViewDetailed
	Userpic string
	Banner  string
}

func Profile(ctx context.Context, profile *bsky.ActorDefs_ProfileViewDetailed, uploader Uploader) (string, error) {
	log := zerolog.Ctx(ctx)
	data := &profileData{
		ActorDefs_ProfileViewDetailed: profile,
	}

	if profile.Avatar != nil {
		resp, err := http.Get(*profile.Avatar)
		if err != nil {
			log.Warn().Err(err).Msgf("Failed to fetch user avatar: %s", err)
		} else {
			b, err := io.ReadAll(resp.Body)
			resp.Body.Close()
			if err == nil {
				uploaded, err := uploader.Upload(ctx, "userpic.png", b)
				if err == nil {
					data.Userpic = fmt.Sprintf("/attachments/download/%d/%s", uploaded.Id, "userpic.png")
				}
			}
		}
	}
	if profile.Banner != nil {
		resp, err := http.Get(*profile.Banner)
		if err != nil {
			log.Warn().Err(err).Msgf("Failed to fetch user avatar: %s", err)
		} else {
			b, err := io.ReadAll(resp.Body)
			resp.Body.Close()
			if err == nil {
				uploaded, err := uploader.Upload(ctx, "banner.png", b)
				if err == nil {
					data.Banner = fmt.Sprintf("/attachments/download/%d/%s", uploaded.Id, "banner.png")
				}
			}
		}
	}

	w := bytes.NewBuffer(nil)
	if err := profileTemplate.Execute(w, data); err != nil {
		return "", err
	}
	return w.String(), nil
}
