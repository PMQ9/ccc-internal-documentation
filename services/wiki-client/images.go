package wikiclient

import (
	"context"
	"io"
	"net/http"
	"strconv"
)

// UploadImage uploads an image to the gallery via POST /api/image-gallery (multipart:
// type, uploaded_to, name, image). imgType is "gallery" or "drawio". It reads the image
// content from r; fileName is the multipart filename. The image lands on the persistent
// media volume. Returns the stored Image (with its URL/path).
func (c *Client) UploadImage(ctx context.Context, pageID int64, name, imgType, fileName string, r io.Reader) (Image, error) {
	var img Image
	fields := map[string]string{
		"type":        imgType,
		"uploaded_to": strconv.FormatInt(pageID, 10),
		"name":        name,
	}
	err := c.doUpload(ctx, http.MethodPost, "/api/image-gallery", fields, "image", fileName, r, &img)
	return img, err
}
