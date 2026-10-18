//go:build unix

package main

import (
	"bufio"
	"context"
	_ "embed"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/alexbathome/unsparse"
	"github.com/charmbracelet/fang"
	mango "github.com/muesli/mango-cobra"
	"github.com/muesli/roff"
	"github.com/spf13/cobra"
)

//go:embed unspar.magic
var magicRules string

func main() {
	var (
		sparse, force bool
		displayMagic  bool
		displayMan    bool
		theme         = fang.WithColorSchemeFunc(fang.AnsiColorScheme)
	)

	cmd := &cobra.Command{
		Use:   "unspar INPUT OUTPUT",
		Short: "Convert between sparse files and the unsparsed format",
		Long: `Convert between sparse files and the unsparsed format.

By default the input is a sparse file, which is written to the output file in the unsparsed format (the sparse file with its holes removed). With -s the input is an unsparsed file, which is written to the output file as a sparse file.`,
		Example: `  # unsparse a sparse file
  unspar sparsedfile unsparsedfile

  # sparse an unsparsed file
  unspar unsparsedfile sparsedfile -s

  # try the magic(5) rules with file(1)
  unspar --magic > unspar.magic
  file -m unspar.magic unsparsedfile

  # view the man page
  unspar --man | mandoc -a`,
		Args: func(cmd *cobra.Command, args []string) error {
			if displayMagic || displayMan {
				return cobra.NoArgs(cmd, args)
			}
			return cobra.ExactArgs(2)(cmd, args)
		},
		SilenceErrors: false,
		RunE: func(cmd *cobra.Command, args []string) error {
			switch {
			case displayMagic:
				_, err := io.WriteString(cmd.OutOrStdout(), magicRules)
				return err
			case displayMan:
				page, err := mango.NewManPage(1, cmd)
				if err != nil {
					return err
				}
				_, err = io.WriteString(cmd.OutOrStdout(), page.Build(roff.NewDocument()))
				return err
			}
			if sparse {
				return runSparse(args[0], args[1], force)
			}
			return runUnsparse(args[0], args[1], force)
		},
	}
	cmd.Flags().BoolVarP(&sparse, "sparse", "s", false, "convert an unsparsed file back into a sparse file")

	cmd.Flags().BoolVarP(&force, "force", "f", false, "overwrite an existing output file, and unsparse input that has no holes")

	cmd.Flags().BoolVar(&displayMagic, "magic", false, "print the magic(5) rules for the unsparsed format to stdout")
	cmd.Flags().BoolVar(&displayMan, "man", false, "print the man page to stdout")
	cmd.MarkFlagsMutuallyExclusive("magic", "man")

	if err := fang.Execute(
		context.Background(),
		cmd,
		fang.WithoutCompletions(),
		fang.WithErrorHandler(errorHandler),
		fang.WithoutManpage(), // replaced by --man
		theme,
	); err != nil {
		os.Exit(1)
	}
}

// errorHandler wraps fang's default handler, dropping its error text
// transform. The default one title-cases the first word, which mangles
// messages that start with a filename ("main.go" -> "Main.go").
func errorHandler(w io.Writer, styles fang.Styles, err error) {
	styles.ErrorText = styles.ErrorText.UnsetTransform()
	fang.DefaultErrorHandler(w, styles, err)
}

func runUnsparse(inPath, outPath string, force bool) error {
	in, info, err := openInput(inPath)
	if err != nil {
		return err
	}
	defer in.Close()

	if !force && !hasHoles(info) {
		return fmt.Errorf("%s has no holes, so it is not a sparse file (use --force to convert it anyway)", inPath)
	}

	sr, err := unsparse.SparseReaderFile(in)
	if err != nil {
		return err
	}
	return writeOutput(inPath, outPath, force, func(out *os.File) error {
		return unsparse.Unsparse(out, sr)
	})
}

func runSparse(inPath, outPath string, force bool) error {
	in, _, err := openInput(inPath)
	if err != nil {
		return err
	}
	defer in.Close()

	if err := validateUnsparsed(in); err != nil {
		return fmt.Errorf("%s is not a valid unsparsed file: %w", inPath, err)
	}
	if _, err := in.Seek(0, io.SeekStart); err != nil {
		return err
	}
	return writeOutput(inPath, outPath, force, func(out *os.File) error {
		return unsparse.Sparse(out, in)
	})
}

// openInput opens path, which must be a regular file.
func openInput(path string) (*os.File, os.FileInfo, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, nil, err
	}
	if !info.Mode().IsRegular() {
		f.Close()
		return nil, nil, fmt.Errorf("%s is not a regular file", path)
	}
	return f, info, nil
}

// writeOutput runs write against a temporary file next to outPath and renames
// it into place on success, so a failure never damages an existing output. It
// refuses to replace the input, or an existing file without force.
func writeOutput(inPath, outPath string, force bool, write func(*os.File) error) error {
	if existing, err := os.Stat(outPath); err == nil {
		in, err := os.Stat(inPath)
		if err != nil {
			return err
		}
		if os.SameFile(in, existing) {
			return fmt.Errorf("input and output are the same file: %s", outPath)
		}
		if !existing.Mode().IsRegular() {
			return fmt.Errorf("%s exists and is not a regular file", outPath)
		}
		if !force {
			return fmt.Errorf("%s already exists (use --force to overwrite it)", outPath)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}

	tmp, err := os.CreateTemp(filepath.Dir(outPath), ".unspar-*")
	if err != nil {
		return fmt.Errorf("creating output: %w", err)
	}
	defer os.Remove(tmp.Name()) // no-op once renamed

	if err := write(tmp); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o644); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), outPath)
}

// validateUnsparsed checks that r is exactly an unsparsed file: a valid header
// (magic, version) followed by well-formed extents that run to the end of the file.
func validateUnsparsed(r io.Reader) error {
	br := bufio.NewReader(r)

	hdr, err := unsparse.ReadHeader(br)
	if err != nil {
		return err
	}

	var end int64
	for {
		var e unsparse.Extant
		err := binary.Read(br, binary.BigEndian, &e)
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("reading extent header: %w", err)
		}
		if e.Offset < end || e.Size < 0 || e.Size > hdr.ApparentSize-e.Offset {
			return fmt.Errorf("invalid extent %+v", e)
		}
		if n, err := io.CopyN(io.Discard, br, e.Size); err != nil {
			return fmt.Errorf("extent at %d is short by %d bytes", e.Offset, e.Size-n)
		}
		end = e.Offset + e.Size
	}
}
