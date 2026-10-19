package telegram

import (
	"context"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestSendPhotoUploadAndReuse: the first send uploads the bytes (multipart
// with caption, parse mode and keyboard), later sends pass the file_id.
func TestSendPhotoUploadAndReuse(t *testing.T) {
	var (
		gotUpload  map[string]string
		gotFile    []byte
		gotJSON    map[string]any
		gotHeaders []string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/bot"+testToken+"/sendPhoto") {
			http.NotFound(w, r)
			return
		}
		ct := r.Header.Get("Content-Type")
		gotHeaders = append(gotHeaders, ct)
		mt, params, _ := mime.ParseMediaType(ct)
		if mt == "multipart/form-data" {
			gotUpload = map[string]string{}
			mr := multipart.NewReader(r.Body, params["boundary"])
			for {
				part, err := mr.NextPart()
				if err != nil {
					break
				}
				b, _ := io.ReadAll(part)
				if part.FormName() == "photo" {
					gotFile = b
					gotUpload["filename"] = part.FileName()
				} else {
					gotUpload[part.FormName()] = string(b)
				}
			}
		} else {
			_ = json.NewDecoder(r.Body).Decode(&gotJSON)
		}
		writeJSON(w, 200, `{"ok":true,"result":{"message_id":9,"chat":{"id":42,"type":"private"},"date":1,
			"photo":[{"file_id":"small","file_unique_id":"s","width":90,"height":90},
			         {"file_id":"big","file_unique_id":"b","width":800,"height":800}]}}`)
	}))
	t.Cleanup(srv.Close)
	c := New(testToken, WithBaseURL(srv.URL), WithHTTPClient(srv.Client()))
	markup := &InlineKeyboardMarkup{InlineKeyboard: [][]InlineKeyboardButton{{{Text: "Map", WebApp: &WebAppInfo{URL: "https://a.kz/p"}}}}}

	m, err := c.SendPhoto(context.Background(), SendPhotoParams{ChatID: 42, Photo: InputPhoto{Data: []byte("\xff\xd8jpeg")},
		Caption: "<b>Driver</b>", ParseMode: "HTML", ReplyMarkup: markup})
	if err != nil {
		t.Fatal(err)
	}
	if m.MessageID != 9 || m.LargestPhoto() != "big" {
		t.Fatalf("message %+v", m)
	}
	if string(gotFile) != "\xff\xd8jpeg" || gotUpload["chat_id"] != "42" || gotUpload["caption"] != "<b>Driver</b>" ||
		gotUpload["parse_mode"] != "HTML" || !strings.Contains(gotUpload["reply_markup"], `"web_app":{"url":"https://a.kz/p"}`) ||
		gotUpload["filename"] != "photo.jpg" {
		t.Fatalf("upload: %v", gotUpload)
	}

	if _, err := c.SendPhoto(context.Background(), SendPhotoParams{ChatID: 42, Photo: InputPhoto{FileID: "big"}, Caption: "x"}); err != nil {
		t.Fatal(err)
	}
	if gotJSON["photo"] != "big" || gotJSON["chat_id"] != float64(42) || !strings.HasPrefix(gotHeaders[1], "application/json") {
		t.Fatalf("file id send: %v %v", gotJSON, gotHeaders)
	}
	if _, err := c.SendPhoto(context.Background(), SendPhotoParams{ChatID: 42}); err == nil {
		t.Fatal("a send without a photo was accepted")
	}
}
