package attachments

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"bsky.watch/redmine"
)

type AttachmentCreator struct {
	client      *redmine.Client
	attachments []*redmine.Upload
}

func NewGlobalAttachmentCreator(client *redmine.Client) *AttachmentCreator {
	return &AttachmentCreator{client: client}
}

func (c *AttachmentCreator) Upload(ctx context.Context, filename string, content []byte) (*redmine.Upload, error) {
	var err error
	if content == nil {
		content, err = os.ReadFile(filename)
		if err != nil {
			return nil, fmt.Errorf("failed to open %q: %w", filename, err)
		}
	}

	f, err := os.CreateTemp("", fmt.Sprintf("*.%s", filepath.Base(filename)))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	defer os.Remove(f.Name())
	if _, err := f.Write(content); err != nil {
		return nil, err
	}
	if _, err := f.Seek(0, 0); err != nil {
		return nil, err
	}

	uploaded, err := c.client.Upload(f.Name())
	if err != nil {
		return nil, fmt.Errorf("Upload: %w", err)
	}
	uploaded.Filename = filepath.Base(filename)
	c.attachments = append(c.attachments, uploaded)
	return uploaded, nil
}

func (c *AttachmentCreator) Created() []*redmine.Upload {
	return c.attachments
}
