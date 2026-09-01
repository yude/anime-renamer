package annict

import (
	"encoding/json"
	"testing"
)

func TestEpisodeUnmarshalNumberRepresentations(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		wantNumber *float64
		wantErr    bool
	}{
		{name: "GraphQL numeric integer", input: `{"id":1,"number":7,"title":"ep7"}`, wantNumber: numberPtr(7)},
		{name: "REST quoted integer", input: `{"id":1,"number":"7","title":"ep7"}`, wantNumber: numberPtr(7)},
		{name: "REST quoted fractional", input: `{"id":1,"number":"7.5","title":"special"}`, wantNumber: numberPtr(7.5)},
		{name: "explicit null", input: `{"id":1,"number":null}`, wantNumber: nil},
		{name: "missing number", input: `{"id":1}`, wantNumber: nil},
		{name: "invalid quoted number", input: `{"id":1,"number":"第七話"}`, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var episode Episode
			err := json.Unmarshal([]byte(tt.input), &episode)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("json.Unmarshal(%s) succeeded, want error", tt.input)
				}
				return
			}
			if err != nil {
				t.Fatalf("json.Unmarshal(%s) error = %v", tt.input, err)
			}
			if episode.ID != 1 {
				t.Errorf("Episode.ID = %d, want 1", episode.ID)
			}
			if tt.wantNumber == nil {
				if episode.Number != nil {
					t.Errorf("Episode.Number = %v, want nil", *episode.Number)
				}
				return
			}
			if episode.Number == nil || *episode.Number != *tt.wantNumber {
				t.Errorf("Episode.Number = %v, want %v", episode.Number, *tt.wantNumber)
			}
		})
	}
}

func numberPtr(number float64) *float64 {
	return &number
}
