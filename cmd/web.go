package cmd

import (
	"log/slog"

	"go.uber.org/fx"

	financeweb "github.com/maxjkfc/maxos/app/finance/adapter/in/web"
	"github.com/maxjkfc/maxos/pkg/webserver"
)

// webRegistrarGroup is the Fx value group that collects the page registrar of every
// context, as registrarGroup does for the MCP tools. Adding a context's pages to the
// site means providing its registrar into this group.
const webRegistrarGroup = `group:"web.registrars"`

// WebModule provides the registry of the pages under /app/, fed by the registrar of
// every context that has pages. The pages call the use cases of the contexts, so the
// app must include CatalogModule and FinanceModule too; they tell today by the clock
// that InfraModule provides.
func WebModule() fx.Option {
	return fx.Provide(
		fx.Annotate(financeweb.NewRegistrar, fx.ResultTags(webRegistrarGroup)),
		fx.Annotate(newWebRegistry, fx.ParamTags(``, webRegistrarGroup)),
	)
}

func newWebRegistry(logger *slog.Logger, registrars []webserver.Registrar) *webserver.Registry {
	return webserver.NewRegistry(logger, registrars)
}
