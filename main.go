package main

import (
	"context"
	"encoding/base64"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/go-kratos/kratos/v2"
	"github.com/go-kratos/kratos/v2/config"
	"github.com/go-kratos/kratos/v2/config/file"
	"github.com/go-kratos/kratos/v2/log"
	"github.com/libp2p/go-libp2p/core/crypto"

	api_transport "github.com/omalloc/balefire/api/transport"
	"github.com/omalloc/balefire/conf"
	"github.com/omalloc/balefire/storage"
	"github.com/omalloc/balefire/transport"
)

var (
	_               = ""
	flagConf string = "config.yaml"
	flagPkey bool

	id, _   = os.Hostname()
	name    = "balefire"
	version = "v0.1.0"
)

func init() {
	flag.StringVar(&flagConf, "conf", "config.yaml", "")
	flag.BoolVar(&flagPkey, "pk", false, "")

	log.SetLogger(log.With(log.DefaultLogger,
		"ts", log.Timestamp(time.DateTime),
		// "service.id", id,
		"service.name", name,
		// "service.version", version,
	))
}

func main() {
	flag.Parse()

	// generate keypair
	if flagPkey {
		privBytes, pubBytes, err := GenerateKeyPair()
		if err != nil {
			fmt.Printf("failed generate ED25519 KEY: %v", err)
			os.Exit(1)
			return
		}

		fmt.Printf("Private Key:\n%s\n", base64.StdEncoding.EncodeToString(privBytes))
		fmt.Printf("Public Key:\n%s\n", base64.StdEncoding.EncodeToString(pubBytes))
		return
	}

	c := config.New(config.WithSource(file.NewSource(flagConf)))
	defer c.Close()

	if err := c.Load(); err != nil {
		panic(err)
	}

	var bc conf.Bootstrap
	if err := c.Scan(&bc); err != nil {
		panic(err)
	}

	app, err := newApp(&bc)
	if err != nil {
		panic(err)
	}

	if err := app.Run(); err != nil {
		panic(err)
	}
}

func newApp(bc *conf.Bootstrap) (*kratos.App, error) {
	// new transport
	tr, err := transport.NewP2PTransport(transport.Option{
		Mode:         api_transport.Mode(bc.Transport.Mode),
		Identity:     bc.Transport.PrivateKey,
		ListenAddrs:  bc.Transport.ListenAddrs,
		CentralPeers: bc.Transport.Peers,
	})
	if err != nil {
		return nil, err
	}

	_ = os.MkdirAll(bc.Storage.Path, 0o755)

	// new storage
	store, err := storage.NewNutsDBStore(bc.Storage.Path)
	if err != nil {
		return nil, err
	}

	// new outboxService
	outboxService := transport.NewOutboxService(tr, store)

	app := kratos.New(
		kratos.Name(name),
		kratos.Version(version),
		kratos.Metadata(map[string]string{}),
		kratos.Logger(log.GetLogger()),
		kratos.Server(
			tr,
			outboxService,
		),
		kratos.AfterStart(func(_ context.Context) error {
			log.Infof("%s started", name)
			return nil
		}),
		kratos.AfterStop(func(ctx context.Context) error {
			return store.Close()
		}),
	)

	return app, nil
}

func GenerateKeyPair() ([]byte, []byte, error) {
	pk, uk, _ := crypto.GenerateEd25519Key(nil)

	privBytes, err := crypto.MarshalPrivateKey(pk)
	if err != nil {
		return nil, nil, err
	}

	pubBytes, err := crypto.MarshalPublicKey(uk)
	if err != nil {
		return nil, nil, err
	}

	return privBytes, pubBytes, nil
}
