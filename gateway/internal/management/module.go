// Package management assembles the gateway's private management module.
package management

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"sync"
	"time"

	"dnf90.local/gateway/internal/modules/dnf/adminapi"
	dnfpvf "dnf90.local/gateway/internal/modules/dnf/pvf"
	"dnf90.local/gateway/internal/modules/dnf/repository"
	"dnf90.local/gateway/internal/modules/dnf/repository/mysql"
	"dnf90.local/gateway/internal/modules/dnf/testbenefits"
	platformpvf "dnf90.local/gateway/internal/platform/pvf"
	"dnf90.local/gateway/internal/services/gmtool"
)

type Module struct {
	Handler http.Handler
	server  *gmtool.Server
	archive *platformpvf.DiskArchive
	pools   map[string]*sql.DB
	cancel  context.CancelFunc
	done    chan struct{}
	once    sync.Once
}

func Open(ctx context.Context, cfg Config, log func(string, ...any)) (_ *Module, result error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if log == nil {
		log = func(string, ...any) {}
	}
	workerCtx, cancel := context.WithCancel(ctx)
	m := &Module{pools: make(map[string]*sql.DB), cancel: cancel, done: make(chan struct{})}
	started := false
	defer func() {
		if result != nil {
			if !started {
				close(m.done)
			}
			m.Close()
		}
	}()
	var err error
	var source dnfpvf.Source
	var catalog *gmtool.RemoteCatalog
	if cfg.Catalog != nil {
		catalog, err = gmtool.NewRemoteCatalog(*cfg.Catalog)
		if err != nil {
			return nil, err
		}
		source = catalog
	} else {
		m.archive, err = platformpvf.OpenDisk(cfg.PVFPath, cfg.DiskIndexPath(), cfg.PVFSHA256)
		if err != nil {
			return nil, errors.New("management PVF disk index unavailable or mismatched")
		}
		source = m.archive
	}
	pool := func(dsn string) (*sql.DB, error) {
		if db := m.pools[dsn]; db != nil {
			return db, nil
		}
		db, e := sql.Open("mysql", dsn)
		if e != nil {
			return nil, errors.New("management database configuration invalid")
		}
		db.SetMaxOpenConns(8)
		db.SetMaxIdleConns(2)
		db.SetConnMaxLifetime(5 * time.Minute)
		m.pools[dsn] = db
		check, stop := context.WithTimeout(ctx, 5*time.Second)
		defer stop()
		if e = db.PingContext(check); e != nil {
			return nil, errors.New("management database unavailable")
		}
		return db, nil
	}
	registration, err := pool(cfg.RegistrationDSN)
	if err != nil {
		return nil, err
	}
	data, err := pool(cfg.RepositoryDSN)
	if err != nil {
		return nil, err
	}
	if err = mysql.CheckManagementSchema(ctx, data, mysql.MySQLGroupOptions{TablePrefix: cfg.TablePrefix, DatabasePlan: repository.DatabasePlan{WriteDatabases: cfg.Databases, ReadDatabases: cfg.Databases}}); err != nil {
		return nil, errors.New("management receipt schema unavailable; run --init-management-schema")
	}
	group, err := mysql.NewMySQLGroupFromDB(data, mysql.MySQLGroupOptions{TablePrefix: cfg.TablePrefix, DatabasePlan: repository.DatabasePlan{WriteDatabases: cfg.Databases, ReadDatabases: cfg.Databases}})
	if err != nil {
		return nil, errors.New("management repository mapping invalid")
	}
	benefits, err := testbenefits.NewStore(registration, cfg.AccountTable)
	if err != nil {
		return nil, err
	}
	if _, err = benefits.Campaigns(ctx); err != nil {
		return nil, errors.New("management benefit schema unavailable; initialize during installation")
	}
	if err = benefits.CheckPVFReviewSchema(ctx); err != nil {
		return nil, errors.New("management benefit PVF history schema unavailable; run --init-management-schema")
	}
	runtime, err := adminapi.NewClient(cfg.GameAPI, cfg.GameToken)
	if err != nil {
		return nil, err
	}
	deps := gmtool.Deps{Catalog: catalog, RegistrationDB: registration, Runtime: runtime, Benefits: benefits, Repos: func() (repository.Group, bool) { return group, true }, PVF: func() (dnfpvf.Source, error) { return source, nil }}
	deps.PremiumTypes = func() ([]int64, error) {
		r, e := runtime.Call(workerCtx, adminapi.Request{Action: "catalog"})
		return r.PremiumTypes, e
	}
	deps.CrystalCubeItemIDs = func() ([6]int64, error) {
		r, e := runtime.Call(workerCtx, adminapi.Request{Action: "catalog"})
		return r.CrystalCubeItemIDs, e
	}
	secret := cfg.Password
	if cfg.LoginKeySHA256 != "" {
		secret = cfg.LoginKeySHA256
	}
	m.server, err = gmtool.NewManaged(ctx, gmtool.Config{Password: secret, LoginKeySHA256: cfg.LoginKeySHA256, SessionStorePath: cfg.SessionStorePath, Table: cfg.AccountTable, PVFPath: cfg.PVFPath, ItemIndexCachePath: cfg.ItemIndexPath, DatabaseLabel: "MariaDB", RequireOffline: true, Logger: log}, deps)
	if err != nil {
		return nil, err
	}
	if cfg.Catalog != nil {
		m.Handler = m.server.GatewayHandler(cfg.ServiceToken, cfg.PVFSHA256)
		close(m.done)
		return m, nil
	}
	m.Handler = m.server.ManagementHandler(cfg.WebRoot)
	started = true
	go func() {
		defer close(m.done)
		timer := time.NewTimer(cfg.scanInterval())
		defer timer.Stop()
		var cursor int64
		for {
			select {
			case <-workerCtx.Done():
				return
			case <-timer.C:
				scan, stop := context.WithTimeout(workerCtx, 20*time.Second)
				var e error
				cursor, e = m.server.ScanNewCharacters(scan, cursor, 50)
				stop()
				if e != nil && workerCtx.Err() == nil {
					log("management-benefit-scan-retry")
				}
				timer.Reset(cfg.scanInterval())
			}
		}
	}()
	return m, nil
}

func (m *Module) Close() error {
	var result error
	m.once.Do(func() {
		m.cancel()
		<-m.done
		if m.server != nil {
			result = errors.Join(result, m.server.Stop())
		}
		for _, db := range m.pools {
			result = errors.Join(result, db.Close())
		}
		if m.archive != nil {
			result = errors.Join(result, m.archive.Close())
		}
	})
	return result
}

// HubSessionGuard shares the integrated management session with Hub settings.
func (m *Module) HubSessionGuard(next http.Handler) http.Handler {
	return m.server.HubHandler(next)
}
