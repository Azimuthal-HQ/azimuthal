package db

import (
	"github.com/Azimuthal-HQ/azimuthal/migrations"
)

func init() {
	MigrationFS = migrations.FS
}
