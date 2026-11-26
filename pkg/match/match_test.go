package match

import (
	"testing"

	"github.com/mfr411/boxvector/pkg/util"
)

func TestCommonIXs(t *testing.T) {
	util.NeedsInternet(t)
	CommonIXs(34553, 112, false, 10, "")
}
