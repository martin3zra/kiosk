package kiosk

import (
	"net/http"
	"strings"

	"github.com/martin3zra/forge/routing"
	"github.com/martin3zra/playsql"
)

// rolesIndex handles GET /kiosks/roles — the fixed global list (plus
// whatever a host project's own migrations added to it), for populating a
// role picker in the assign-user / create-company forms.
func rolesIndex(db *playsql.DB) routing.HandlerFunc {
	return func(ctx *routing.Context) {
		var rows []roleModel
		if err := db.Model(&roleModel{}).OrderBy("name", playsql.Asc).Get(ctx.Request.Context(), &rows); err != nil {
			ctx.Error(err)
			return
		}
		ctx.JSON(http.StatusOK, rows)
	}
}

// findRoleByName resolves a role name ("owner", "admin", or one a host
// project's own migration added) to its row. Matching is case-insensitive;
// names are stored lowercase by convention (see the seed migration) but
// this doesn't enforce that on rows a host project inserts itself.
func findRoleByName(ctx *routing.Context, db *playsql.DB, name string) (roleModel, error) {
	var row roleModel
	err := db.Model(&roleModel{}).WhereEq("name", strings.ToLower(strings.TrimSpace(name))).First(ctx.Request.Context(), &row)
	return row, err
}
