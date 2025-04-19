// Copyright (C) 2025 wangyusong
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Affero General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with this program. If not, see <https://www.gnu.org/licenses/>.

package http

import (
	"net"
	"net/http"

	"github.com/pkg/errors"

	"github.com/glidea/zenfeed/pkg/api"
	"github.com/glidea/zenfeed/pkg/component"
	"github.com/glidea/zenfeed/pkg/config"
	telemetry "github.com/glidea/zenfeed/pkg/telemetry"
	"github.com/glidea/zenfeed/pkg/telemetry/log"
	"github.com/glidea/zenfeed/pkg/telemetry/metric"
	telemetrymodel "github.com/glidea/zenfeed/pkg/telemetry/model"
	"github.com/glidea/zenfeed/pkg/util/rpc"
)

// --- Interface code block ---
type Server interface {
	component.Component
	config.Watcher
}

type Config struct {
	Address string
}

func (c *Config) Validate() error {
	if c.Address == "" {
		c.Address = ":1300"
	}
	if _, _, err := net.SplitHostPort(c.Address); err != nil {
		return errors.Wrap(err, "invalid address")
	}

	return nil
}

func (c *Config) From(app *config.App) *Config {
	c.Address = app.API.HTTP.Address

	return c
}

type Dependencies struct {
	API api.API
}

// --- Factory code block ---
type Factory component.Factory[Server, config.App, Dependencies]

func NewFactory(mockOn ...component.MockOption) Factory {
	if len(mockOn) > 0 {
		return component.FactoryFunc[Server, config.App, Dependencies](
			func(instance string, config *config.App, dependencies Dependencies) (Server, error) {
				m := &mockServer{}
				component.MockOptions(mockOn).Apply(&m.Mock)

				return m, nil
			},
		)
	}

	return component.FactoryFunc[Server, config.App, Dependencies](new)
}

func new(instance string, app *config.App, dependencies Dependencies) (Server, error) {
	config := &Config{}
	config.From(app)
	if err := config.Validate(); err != nil {
		return nil, errors.Wrap(err, "validate config")
	}

	router := http.NewServeMux()
	api := dependencies.API
	router.Handle("/metrics", metric.Handler())
	router.Handle("/health", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	router.Handle("/write", rpc.API(api.Write))
	router.Handle("/query_config", rpc.API(api.QueryAppConfig))
	router.Handle("/apply_config", rpc.API(api.ApplyAppConfig))
	router.Handle("/query_config_schema", rpc.API(api.QueryAppConfigSchema))
	router.Handle("/query_rsshub_categories", rpc.API(api.QueryRSSHubCategories))
	router.Handle("/query_rsshub_websites", rpc.API(api.QueryRSSHubWebsites))
	router.Handle("/query_rsshub_routes", rpc.API(api.QueryRSSHubRoutes))
	router.Handle("/query", rpc.API(api.Query))
	httpServer := &http.Server{Addr: config.Address, Handler: router}

	return &server{
		Base: component.New(&component.BaseConfig[Config, Dependencies]{
			Name:         "HTTPServer",
			Instance:     instance,
			Config:       config,
			Dependencies: dependencies,
		}),
		http: httpServer,
	}, nil
}

// --- Implementation code block ---
type server struct {
	*component.Base[Config, Dependencies]
	http *http.Server
}

func (s *server) Run() (err error) {
	ctx := telemetry.StartWith(s.Context(), append(s.TelemetryLabels(), telemetrymodel.KeyOperation, "Run")...)
	defer func() { telemetry.End(ctx, err) }()

	serverErr := make(chan error, 1)
	go func() {
		serverErr <- s.http.ListenAndServe()
	}()

	s.MarkReady()
	select {
	case <-ctx.Done():
		log.Info(ctx, "shutting down")

		return s.http.Shutdown(ctx)
	case err := <-serverErr:
		return errors.Wrap(err, "listen and serve")
	}
}

func (s *server) Reload(app *config.App) error {
	newConfig := &Config{}
	newConfig.From(app)
	if err := newConfig.Validate(); err != nil {
		return errors.Wrap(err, "validate config")
	}
	if s.Config().Address != newConfig.Address {
		return errors.New("address cannot be reloaded")
	}

	s.SetConfig(newConfig)

	return nil
}

type mockServer struct {
	component.Mock
}

func (m *mockServer) Reload(app *config.App) error {
	return m.Called(app).Error(0)
}
