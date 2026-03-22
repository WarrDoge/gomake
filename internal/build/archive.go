package build

import (
	"bytes"
	"errors"
	"io"
	"os"
	"strconv"
	"strings"
	"time"
)

var errArchiveMemberNotFound = errors.New("archive member not found")

// statArchiveMember reads an ar file and returns the modtime of the specified member.
func statArchiveMember(archivePath, memberName string) (time.Time, error) {
	f, err := os.Open(archivePath)
	if err != nil {
		return time.Time{}, err
	}
	defer f.Close()

	magic := make([]byte, 8)
	if _, err := io.ReadFull(f, magic); err != nil {
		return time.Time{}, err
	}
	if string(magic) != "!<arch>\n" {
		return time.Time{}, errors.New("not an ar archive")
	}

	var stringTable []byte

	for {
		header := make([]byte, 60)
		if _, err := io.ReadFull(f, header); err != nil {
			if err == io.EOF {
				break
			}
			return time.Time{}, err
		}

		nameRaw := strings.TrimSpace(string(header[0:16]))
		sizeStr := strings.TrimSpace(string(header[48:58]))
		size, err := strconv.ParseInt(sizeStr, 10, 64)
		if err != nil {
			return time.Time{}, err
		}

		name := nameRaw
		if nameRaw == "//" {
			// GNU string table
			stringTable = make([]byte, size)
			if _, err := io.ReadFull(f, stringTable); err != nil {
				return time.Time{}, err
			}
		} else {
			if strings.HasPrefix(nameRaw, "/") && len(nameRaw) > 1 {
				// GNU extended name
				offset, err := strconv.Atoi(strings.TrimSpace(nameRaw[1:]))
				if err == nil && stringTable != nil && offset < len(stringTable) {
					end := bytes.IndexByte(stringTable[offset:], '/')
					if end >= 0 {
						name = string(stringTable[offset : offset+end])
					}
				}
			} else {
				// standard name
				name = strings.TrimRight(nameRaw, "/")
			}

			if name == memberName {
				dateStr := strings.TrimSpace(string(header[16:28]))
				timestamp, err := strconv.ParseInt(dateStr, 10, 64)
				if err != nil {
					return time.Time{}, err
				}
				return time.Unix(timestamp, 0), nil
			}

			// skip data
			if _, err := f.Seek(size, io.SeekCurrent); err != nil {
				return time.Time{}, err
			}
		}

		// align to 2 bytes
		if size%2 != 0 {
			if _, err := f.Seek(1, io.SeekCurrent); err != nil {
				return time.Time{}, err
			}
		}
	}

	return time.Time{}, errArchiveMemberNotFound
}

// parseArchiveTarget parses a target name like "lib.a(obj.o)" into archive and member parts.
func parseArchiveTarget(name string) (archive, member string, isArchive bool) {
	if !strings.HasSuffix(name, ")") {
		return "", "", false
	}
	idx := strings.IndexByte(name, '(')
	if idx < 0 {
		return "", "", false
	}
	return name[:idx], name[idx+1 : len(name)-1], true
}
