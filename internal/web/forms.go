package web

import (
	"errors"
	"io"
	"mime"
	"net/http"
)

// mutationMaxBody also applies to actions without fields: no mutation route
// should accept an unbounded body. Multipart is supported for JS FormData.
const mutationMaxBody = 64 << 10

func mutationForm(next http.HandlerFunc, limit int64) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.ContentLength > limit {
			http.Error(w, "form body too large", http.StatusRequestEntityTooLarge)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, limit)
		contentType := r.Header.Get("Content-Type")
		mediaType := ""
		var err error
		if contentType != "" {
			mediaType, _, err = mime.ParseMediaType(contentType)
		}
		if err == nil {
			switch mediaType {
			case "multipart/form-data":
				err = r.ParseMultipartForm(limit)
			case "application/x-www-form-urlencoded":
				err = r.ParseForm()
			case "":
				// Empty forms from non-browser clients need no Content-Type.
				var n int64
				n, err = io.Copy(io.Discard, r.Body)
				if err == nil && n != 0 {
					http.Error(w, "unsupported form content type", http.StatusUnsupportedMediaType)
					return
				}
				if err == nil {
					err = r.ParseForm()
				}
			default:
				http.Error(w, "unsupported form content type", http.StatusUnsupportedMediaType)
				return
			}
		}
		if r.MultipartForm != nil {
			defer func() { _ = r.MultipartForm.RemoveAll() }()
		}
		if err != nil {
			status := http.StatusBadRequest
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				status = http.StatusRequestEntityTooLarge
			}
			// Parser errors can contain attacker-supplied bytes; keep the
			// response explicit but do not reflect raw errors or secrets.
			http.Error(w, "invalid form body", status)
			return
		}
		next(w, r)
	}
}
