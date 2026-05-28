package cmd

import (
	"testing"
)

func TestCleanDomain(t *testing.T) {
	tests := []struct {
		input   string
		want    string
		wantErr bool
	}{
		{
			input: "example.com",
			want:  "example.com",
		},
		{
			input: "https://example.com",
			want:  "example.com",
		},
		{
			input: "http://example.com",
			want:  "example.com",
		},
		{
			input: "https://example.com/logmcp",
			want:  "example.com",
		},
		{
			input: "https://example.com:443",
			want:  "example.com",
		},
		{
			input: "example.com:8080",
			want:  "example.com",
		},
		{
			input: "  example.com  ",
			want:  "example.com",
		},
		{
			input:   "",
			wantErr: true,
		},
		{
			input:   "   ",
			wantErr: true,
		},
		{
			input:   "nodot",
			wantErr: true,
		},
		{
			input:   "inval!d.com",
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			got, err := cleanDomain(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Errorf("cleanDomain(%q) = %q, want error", tc.input, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("cleanDomain(%q) unexpected error: %v", tc.input, err)
			}
			if got != tc.want {
				t.Errorf("cleanDomain(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}
