package telegram

import (
	"encoding/json"
	"testing"
)

func TestMessageForwardedAndVenue(t *testing.T) {
	tests := []struct {
		name             string
		raw              string
		forwarded, venue bool
	}{
		{"own pin", `{"message_id":1,"chat":{"id":5,"type":"private"},"date":1,"location":{"latitude":48.8,"longitude":58.1}}`, false, false},
		{"forwarded pin", `{"message_id":2,"chat":{"id":5,"type":"private"},"date":1,"location":{"latitude":48.8,"longitude":58.1},
			"forward_origin":{"type":"user","date":1,"sender_user":{"id":7,"is_bot":false,"first_name":"A"}}}`, true, false},
		{"venue", `{"message_id":3,"chat":{"id":5,"type":"private"},"date":1,"location":{"latitude":48.8,"longitude":58.1},
			"venue":{"location":{"latitude":48.8,"longitude":58.1},"title":"Базар","address":"Ембі"}}`, false, true},
		{"explicit nulls", `{"message_id":4,"chat":{"id":5,"type":"private"},"date":1,"forward_origin":null,"venue":null}`, false, false},
	}
	for _, tc := range tests {
		var m Message
		if err := json.Unmarshal([]byte(tc.raw), &m); err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if m.Forwarded() != tc.forwarded || m.IsVenue() != tc.venue {
			t.Errorf("%s: forwarded=%v venue=%v", tc.name, m.Forwarded(), m.IsVenue())
		}
	}
	// Outgoing encoding is unchanged for ordinary messages.
	b, err := json.Marshal(Message{MessageID: 1, Chat: Chat{ID: 5, Type: "private"}})
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"message_id":1,"chat":{"id":5,"type":"private"},"date":0}`; string(b) != want {
		t.Fatalf("encoded %s, want %s", b, want)
	}
}
