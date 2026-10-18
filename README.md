# unspar 

`unspar` is a CLI tool to re-format sparse files to a regular (non-sparse) file. 

This is especially useful if you need to move sparse files through tools, or systems that do not correctly implement the sparse file format. (e.g [Go's tar.Writer](https://github.com/golang/go/issues/13548))

## Installation

```
go install github.com/alexbathome/unspar@latest
```

## Usage 

Create an unsparse file: 
```bash
unspar file.sparse file.unsparse
```

Create a sparse file from an unsparse file:
```bash
unspar -s file.unsparse file.sparse
```

```
UNSPAR(1)    Convert between sparse files and the unsparsed format   UNSPAR(1)

NAME
       unspar - Convert between sparse files and the unsparsed format

SYNOPSIS
       unspar [options...] [argument...]

DESCRIPTION
       Convert between sparse files and the unsparsed format.

       By default the input is a sparse file, which is written to the output
       file in the unsparsed format (the sparse file with its holes removed).
       With -s the input is an unsparsed file, which is written to the output
       file as a sparse file.

OPTIONS
       --f --force
	      overwrite an existing output file, and unsparse input that has
	      no holes

       --h --help
	      help for unspar

       --magic
	      print the magic(5) rules for the unsparsed format to stdout

       --man  print the man page to stdout

       --s --sparse
	      convert an unsparsed file back into a sparse file

       --v --version
	      version for unspar

unspar				  2026-10-19			     UNSPAR(1)
```
