package edgedbutils

import (
	"context"
	_ "embed"

	"github.com/edgedb/edgedb-go"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

const (
	EDGEDB_DSN = "edgedb://?password_file=/etc/edgedb/password&tls_ca_file_env=EDGEDB_TLS_CA_FILE&user_env=EDGEDB_USER&host_env=EDGEDB_HOST&port_env=EDGEDB_PORT"
)

func GetEDGEDBClient(ctx context.Context, opts edgedb.Options) *edgedb.Client {
	_ = log.FromContext(ctx)
	client, err := edgedb.CreateClientDSN(ctx, EDGEDB_DSN, opts)
	if err != nil {
		log.Log.Error(err, "Error creating edgedb client")
	}
	return client
}

func CloseClient(client *edgedb.Client) {
	err := client.Close()
	if err != nil {
		log.Log.Error(err, "Error closing edgedb client")
	}
}
