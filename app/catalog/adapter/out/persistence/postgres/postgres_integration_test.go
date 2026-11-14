package postgres_test

import (
	"os"
	"testing"

	"github.com/maxjkfc/maxos/internal/testpg"
)

func TestMain(m *testing.M) {
	os.Exit(testpg.Run(m))
}
