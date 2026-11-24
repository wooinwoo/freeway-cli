package update

import (
	"errors"

	"github.com/wooinwoo/freeway-cli/cli/internal/filelock"
)

func lock(path string) (func(), error) {
	release, err := filelock.Try(path)
	if errors.Is(err, filelock.ErrLocked) {
		return nil, errors.New("another installation is in progress")
	}
	return release, err
}
