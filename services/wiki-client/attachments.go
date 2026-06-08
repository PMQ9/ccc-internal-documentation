package wikiclient

import (
	"context"
	"io"
	"net/http"
	"strconv"
)

// UploadAttachment uploads a file attachment to a page via POST /api/attachments
// (multipart: uploaded_to, name, file). It reads the file content from r; fileName is
// the multipart filename BookStack records. The file lands on the persistent media
// volume, not the DB. Returns the stored Attachment (with its server-assigned id).
func (c *Client) UploadAttachment(ctx context.Context, pageID int64, name, fileName string, r io.Reader) (Attachment, error) {
	var a Attachment
	fields := map[string]string{
		"uploaded_to": strconv.FormatInt(pageID, 10),
		"name":        name,
	}
	err := c.doUpload(ctx, http.MethodPost, "/api/attachments", fields, "file", fileName, r, &a)
	return a, err
}
