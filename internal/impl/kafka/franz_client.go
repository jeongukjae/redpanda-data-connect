// Copyright 2024 Redpanda Data, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//    http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package kafka

import (
	"context"
	"crypto/tls"
	"strings"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/sasl"

	"github.com/redpanda-data/benthos/v4/public/service"
)

const (
	// Connection fields
	kfcFieldSeedBrokers            = "seed_brokers"
	kfcFieldClientID               = "client_id"
	kfcFieldTLS                    = "tls"
	kfcFieldMetadataMaxAge         = "metadata_max_age"
	kfcFieldRequestTimeoutOverhead = "request_timeout_overhead"
	kfcFieldConnIdleTimeout        = "conn_idle_timeout"
)

// FranzConnectionFields returns a slice of fields specifically for establishing
// connections to kafka brokers via the franz-go library.
func FranzConnectionFields() []*service.ConfigField {
	return []*service.ConfigField{
		service.NewStringListField(kfcFieldSeedBrokers).
			Description("A list of broker addresses to connect to in order to establish connections. If an item of the list contains commas it will be expanded into multiple addresses.").
			Example([]string{"localhost:9092"}).
			Example([]string{"foo:9092", "bar:9092"}).
			Example([]string{"foo:9092,bar:9092"}),
		service.NewStringField(kfcFieldClientID).
			Description("An identifier for the client connection.").
			Default("benthos").
			Advanced(),
		service.NewTLSToggledField(kfcFieldTLS),
		SASLFields(),
		service.NewDurationField(kfcFieldMetadataMaxAge).
			Description("The maximum age of metadata before it is refreshed.").
			Default("5m").
			Advanced(),
		service.NewDurationField(kfcFieldRequestTimeoutOverhead).
			Description("The request time overhead. Uses the given time as overhead while deadlining requests. Roughly equivalent to request.timeout.ms, but grants additional time to requests that have timeout fields.").
			Default("10s").
			Advanced(),
		service.NewDurationField(kfcFieldConnIdleTimeout).
			Description("The rough amount of time to allow connections to idle before they are closed.").
			Default("20s").
			Advanced(),
	}
}

// FranzConnectionDetails describes information required to create a kafka
// connection.
type FranzConnectionDetails struct {
	SeedBrokers            []string
	ClientID               string
	TLSEnabled             bool
	TLSConf                *tls.Config
	SASL                   []sasl.Mechanism
	MetaMaxAge             time.Duration
	RequestTimeoutOverhead time.Duration
	ConnIdleTimeout        time.Duration

	Logger *service.Logger
}

// FranzConnectionDetailsFromConfig returns a summary of kafka connection
// information, which can be used in order to create a client.
func FranzConnectionDetailsFromConfig(conf *service.ParsedConfig, log *service.Logger) (*FranzConnectionDetails, error) {
	d := FranzConnectionDetails{
		Logger: log,
	}

	brokerList, err := conf.FieldStringList(kfcFieldSeedBrokers)
	if err != nil {
		return nil, err
	}
	for _, b := range brokerList {
		d.SeedBrokers = append(d.SeedBrokers, strings.Split(b, ",")...)
	}

	if d.TLSConf, d.TLSEnabled, err = conf.FieldTLSToggled(kfcFieldTLS); err != nil {
		return nil, err
	}

	if d.SASL, err = SASLMechanismsFromConfig(conf); err != nil {
		return nil, err
	}

	if d.ClientID, err = conf.FieldString(kfcFieldClientID); err != nil {
		return nil, err
	}

	if d.MetaMaxAge, err = conf.FieldDuration(kfcFieldMetadataMaxAge); err != nil {
		return nil, err
	}

	if d.RequestTimeoutOverhead, err = conf.FieldDuration(kfcFieldRequestTimeoutOverhead); err != nil {
		return nil, err
	}

	if d.ConnIdleTimeout, err = conf.FieldDuration(kfcFieldConnIdleTimeout); err != nil {
		return nil, err
	}

	return &d, nil
}

// FranzOpts returns a slice of franz-go opts that establish a connection
// described in the connection details.
func (d *FranzConnectionDetails) FranzOpts() []kgo.Opt {
	opts := []kgo.Opt{
		kgo.WithLogger(&KGoLogger{d.Logger}),
		kgo.SeedBrokers(d.SeedBrokers...),
		kgo.SASL(d.SASL...),
		kgo.ClientID(d.ClientID),
		kgo.MetadataMaxAge(d.MetaMaxAge),
		kgo.RequestTimeoutOverhead(d.RequestTimeoutOverhead),
		kgo.ConnIdleTimeout(d.ConnIdleTimeout),
	}

	if d.TLSEnabled {
		opts = append(opts, kgo.DialTLSConfig(d.TLSConf))
	}

	return opts
}

// FranzConnectionOptsFromConfig returns a slice of franz-go client opts from a
// parsed config.
func FranzConnectionOptsFromConfig(conf *service.ParsedConfig, log *service.Logger) ([]kgo.Opt, error) {
	d, err := FranzConnectionDetailsFromConfig(conf, log)
	if err != nil {
		return nil, err
	}
	return d.FranzOpts(), nil
}

// NewFranzClient attempts to establish a new kafka client, and ensures that
// config errors such as invalid SASL credentials result in the client being
// closed and an error being returned instead of an endless retry loop.
func NewFranzClient(ctx context.Context, opts ...kgo.Opt) (*kgo.Client, error) {
	client, err := kgo.NewClient(opts...)
	if err != nil {
		return nil, err
	}

	if err := client.Ping(ctx); err != nil {
		client.Close()
		if !kgo.IsRetryableBrokerErr(err) {
			return nil, service.NewErrBackOff(err, time.Minute)
		}
		return nil, err
	}

	return client, nil
}
