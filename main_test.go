package main

import (
	"bytes"
	"encoding/binary"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexbathome/unsparse"
)

// unsparsed builds the unsparsed format: the header followed by
// (offset, data) extents.
func unsparsed(size int64, extents ...[]byte) []byte {
	var buf bytes.Buffer
	binary.Write(&buf, binary.BigEndian, unsparse.Unsparsed{Magic: unsparse.Magic, Version: unsparse.Version, ApparentSize: size})
	for i := 0; i+1 < len(extents); i += 2 {
		off := int64(binary.BigEndian.Uint64(extents[i]))
		binary.Write(&buf, binary.BigEndian, off)
		binary.Write(&buf, binary.BigEndian, int64(len(extents[i+1])))
		buf.Write(extents[i+1])
	}
	return buf.Bytes()
}

// headerLen is the size of the unsparsed header: magic, version, flags, size.
const headerLen = 8 + 2 + 2 + 8

// withByte returns a copy of b with b[i] set to v.
func withByte(b []byte, i int, v byte) []byte {
	b = bytes.Clone(b)
	b[i] = v
	return b
}

func offset(n int64) []byte {
	return binary.BigEndian.AppendUint64(nil, uint64(n))
}

func TestRunUnsparse(t *testing.T) {
	tests := []struct {
		name    string
		input   []byte
		missing bool // don't create the input file
		want    []byte
		wantErr bool
	}{
		{
			name:  "empty file",
			input: nil,
			want:  unsparsed(0),
		},
		{
			name:  "dense file is one extent",
			input: []byte("hello"),
			want:  unsparsed(5, offset(0), []byte("hello")),
		},
		{
			name:    "missing input",
			missing: true,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			in := filepath.Join(dir, "in")
			out := filepath.Join(dir, "out")
			if !tt.missing {
				if err := os.WriteFile(in, tt.input, 0o644); err != nil {
					t.Fatal(err)
				}
			}

			err := runUnsparse(in, out, true)
			if (err != nil) != tt.wantErr {
				t.Fatalf("runUnsparse() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			got, err := os.ReadFile(out)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, tt.want) {
				t.Errorf("output = %x, want %x", got, tt.want)
			}
		})
	}
}

func TestRunSparse(t *testing.T) {
	tests := []struct {
		name    string
		input   []byte
		missing bool
		want    []byte
		wantErr bool
	}{
		{
			name:  "empty",
			input: unsparsed(0),
			want:  []byte{},
		},
		{
			name:  "single extent",
			input: unsparsed(5, offset(0), []byte("hello")),
			want:  []byte("hello"),
		},
		{
			name:  "hole between extents reads as zeroes",
			input: unsparsed(10, offset(0), []byte("ab"), offset(8), []byte("cd")),
			want:  []byte("ab\x00\x00\x00\x00\x00\x00cd"),
		},
		{
			name:  "trailing hole extends to apparent size",
			input: unsparsed(6, offset(0), []byte("ab")),
			want:  []byte("ab\x00\x00\x00\x00"),
		},
		{
			name:    "truncated header",
			input:   []byte{1, 2, 3},
			wantErr: true,
		},
		{
			name:    "extent beyond apparent size",
			input:   unsparsed(2, offset(0), []byte("hello")),
			wantErr: true,
		},
		{
			name:    "missing input",
			missing: true,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			in := filepath.Join(dir, "in")
			out := filepath.Join(dir, "out")
			if !tt.missing {
				if err := os.WriteFile(in, tt.input, 0o644); err != nil {
					t.Fatal(err)
				}
			}

			err := runSparse(in, out, true)
			if (err != nil) != tt.wantErr {
				t.Fatalf("runSparse() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			got, err := os.ReadFile(out)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, tt.want) {
				t.Errorf("output = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRoundTrip(t *testing.T) {
	big := make([]byte, 1<<20+5)
	copy(big, "start")
	copy(big[1<<20:], "end!!")

	tests := []struct {
		name    string
		content []byte
	}{
		{"empty", nil},
		{"small", []byte("hello world")},
		{"zeroes", make([]byte, 4096)},
		{"data around a large gap", big},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			orig := filepath.Join(dir, "orig")
			packed := filepath.Join(dir, "packed")
			restored := filepath.Join(dir, "restored")
			if err := os.WriteFile(orig, tt.content, 0o644); err != nil {
				t.Fatal(err)
			}

			if err := runUnsparse(orig, packed, true); err != nil {
				t.Fatalf("runUnsparse() = %v", err)
			}
			if err := runSparse(packed, restored, true); err != nil {
				t.Fatalf("runSparse() = %v", err)
			}
			got, err := os.ReadFile(restored)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, tt.content) {
				t.Errorf("round trip mismatch: got %d bytes, want %d", len(got), len(tt.content))
			}
		})
	}
}

func TestUnwritableOutput(t *testing.T) {
	tests := []struct {
		name string
		run  func(in, out string, force bool) error
		in   []byte
	}{
		{"unsparse", runUnsparse, []byte("data")},
		{"sparse", runSparse, unsparsed(0)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			in := filepath.Join(dir, "in")
			if err := os.WriteFile(in, tt.in, 0o644); err != nil {
				t.Fatal(err)
			}
			out := filepath.Join(dir, "no-such-dir", "out")
			if err := tt.run(in, out, true); err == nil {
				t.Error("expected an error creating output in a missing directory")
			}
		})
	}
}

func TestGuards(t *testing.T) {
	text := []byte("package main\n\nfunc main() {}\n")

	tests := []struct {
		name     string
		sparse   bool
		force    bool
		input    []byte
		out      func(dir, in string) string // output path
		existing []byte                      // pre-existing output content
		wantErr  string
	}{
		{
			name:    "same file",
			input:   text,
			force:   true,
			out:     func(_, in string) string { return in },
			wantErr: "same file",
		},
		{
			name:    "same file via -s",
			sparse:  true,
			input:   unsparsed(0),
			force:   true,
			out:     func(_, in string) string { return in },
			wantErr: "same file",
		},
		{
			name:     "existing output without force",
			sparse:   true,
			input:    unsparsed(0),
			force:    false,
			existing: []byte("precious"),
			wantErr:  "already exists",
		},
		{
			name:     "existing output with force on dense input",
			input:    text,
			force:    true,
			existing: []byte("old"),
		},
		{
			name:    "dense input without force",
			input:   text,
			wantErr: "not a sparse file",
		},
		{
			name:     "-s on a text file leaves existing output alone",
			sparse:   true,
			input:    text,
			force:    true,
			existing: []byte("precious"),
			wantErr:  "not a valid unsparsed file",
		},
		{
			name:    "-s on trailing garbage",
			sparse:  true,
			input:   append(unsparsed(2, offset(0), []byte("ab")), 1, 2, 3),
			wantErr: "not a valid unsparsed file",
		},
		{
			name:    "-s on an old-format file with no magic",
			sparse:  true,
			input:   unsparsed(0)[8:],
			wantErr: "bad magic",
		},
		{
			name:    "-s on an unsupported version",
			sparse:  true,
			input:   withByte(unsparsed(0), 9, 2),
			wantErr: "unsupported version 2",
		},
		{
			name:    "-s on unknown flags",
			sparse:  true,
			input:   withByte(unsparsed(0), 11, 1),
			wantErr: "unsupported flags",
		},
		{
			name:    "-s on a short extent",
			sparse:  true,
			input:   unsparsed(10, offset(0), []byte("abcdef"))[:headerLen+16+3],
			wantErr: "not a valid unsparsed file",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			in := filepath.Join(dir, "in")
			if err := os.WriteFile(in, tt.input, 0o644); err != nil {
				t.Fatal(err)
			}
			out := filepath.Join(dir, "out")
			if tt.out != nil {
				out = tt.out(dir, in)
			}
			if tt.existing != nil {
				if err := os.WriteFile(out, tt.existing, 0o644); err != nil {
					t.Fatal(err)
				}
			}

			run := runUnsparse
			if tt.sparse {
				run = runSparse
			}
			err := run(in, out, tt.force)

			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error = %v, want one containing %q", err, tt.wantErr)
			}

			// A refused conversion must not touch the input or the output.
			if got, _ := os.ReadFile(in); out != in && !bytes.Equal(got, tt.input) {
				t.Errorf("input was modified: %q", got)
			}
			if tt.existing != nil && out != in {
				if got, _ := os.ReadFile(out); !bytes.Equal(got, tt.existing) {
					t.Errorf("existing output was modified: %q", got)
				}
			}
			if out == in {
				if got, _ := os.ReadFile(in); !bytes.Equal(got, tt.input) {
					t.Errorf("input was truncated or modified: %q", got)
				}
			}
			if entries, _ := os.ReadDir(dir); len(entries) > 2 {
				t.Errorf("temporary files left behind: %v", entries)
			}
		})
	}
}

func TestRejectsNonRegularInput(t *testing.T) {
	dir := t.TempDir()
	for name, run := range map[string]func(in, out string, force bool) error{
		"unsparse": runUnsparse,
		"sparse":   runSparse,
	} {
		t.Run(name, func(t *testing.T) {
			err := run(dir, filepath.Join(t.TempDir(), "out"), true)
			if err == nil || !strings.Contains(err.Error(), "not a regular file") {
				t.Errorf("error = %v, want not a regular file", err)
			}
		})
	}
}

func TestUnsparseDetectsHoles(t *testing.T) {
	dir := t.TempDir()
	in := filepath.Join(dir, "in")
	f, err := os.Create(in)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString("start")
	f.Truncate(8 << 20) // grow without writing, which leaves a hole
	f.Close()

	info, _ := os.Stat(in)
	if !hasHoles(info) {
		t.Skip("filesystem does not create sparse files")
	}
	if err := runUnsparse(in, filepath.Join(dir, "out"), false); err != nil {
		t.Errorf("runUnsparse() on a sparse file = %v", err)
	}
}

func TestFileMagic(t *testing.T) {
	filePath, err := exec.LookPath("file")
	if err != nil {
		t.Skip("file(1) not installed")
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "unsparsed")
	if err := os.WriteFile(path, unsparsed(1<<30, offset(0), []byte("hello")), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := exec.Command(filePath, "-b", "-m", "unspar.magic", path).CombinedOutput()
	if err != nil {
		t.Fatalf("file: %v\n%s", err, out)
	}
	want := "Unsparse sparse file data, version 1, apparent size 1073741824 bytes"
	if got := strings.TrimSpace(string(out)); got != want {
		t.Errorf("file output = %q, want %q", got, want)
	}

	// A file without the magic must not match.
	if err := os.WriteFile(path, unsparsed(0)[8:], 0o644); err != nil {
		t.Fatal(err)
	}
	out, _ = exec.Command(filePath, "-b", "-m", "unspar.magic", path).CombinedOutput()
	if strings.Contains(string(out), "Unsparsed") {
		t.Errorf("file matched a file with no magic: %q", out)
	}
}
