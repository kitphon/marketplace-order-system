package main

import (
	"testing"
	"time"
)

func TestLoadConfigDefaults(t *testing.T) {
	t.Setenv("INVENTORY_GRPC_ADDR", "")
	t.Setenv("INVENTORY_EXPIRATION_INTERVAL", "")
	t.Setenv("MONGODB_URI", "")
	t.Setenv("MONGODB_DATABASE", "")

	configuration, err := loadConfig()
	if err != nil {
		t.Fatalf("loadConfig() error = %v", err)
	}
	if configuration.address != defaultAddress ||
		configuration.expirationInterval != defaultExpirationInterval ||
		configuration.mongodbURI != defaultMongoDBURI ||
		configuration.mongodbDatabase != defaultMongoDBDatabase {
		t.Fatalf("loadConfig() = %+v", configuration)
	}
}

func TestLoadConfigOverrides(t *testing.T) {
	t.Setenv("INVENTORY_GRPC_ADDR", "127.0.0.1:55052")
	t.Setenv("INVENTORY_EXPIRATION_INTERVAL", "250ms")
	t.Setenv("MONGODB_URI", "mongodb://database:27017/?replicaSet=rs0")
	t.Setenv("MONGODB_DATABASE", "inventory_test")

	configuration, err := loadConfig()
	if err != nil {
		t.Fatalf("loadConfig() error = %v", err)
	}
	if configuration.address != "127.0.0.1:55052" ||
		configuration.expirationInterval != 250*time.Millisecond ||
		configuration.mongodbURI != "mongodb://database:27017/?replicaSet=rs0" ||
		configuration.mongodbDatabase != "inventory_test" {
		t.Fatalf("loadConfig() = %+v", configuration)
	}
}

func TestLoadConfigRejectsInvalidExpirationInterval(t *testing.T) {
	for _, value := range []string{"invalid", "0", "-1s"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("INVENTORY_GRPC_ADDR", "")
			t.Setenv("INVENTORY_EXPIRATION_INTERVAL", value)
			t.Setenv("MONGODB_URI", "")
			t.Setenv("MONGODB_DATABASE", "")
			if _, err := loadConfig(); err == nil {
				t.Fatal("loadConfig() error = nil, want validation error")
			}
		})
	}
}

func TestLoadConfigRejectsInvalidAddressAndBlankMongoSettings(t *testing.T) {
	tests := []struct {
		name     string
		address  string
		uri      string
		database string
	}{
		{name: "invalid address", address: "127.0.0.1:not-a-port"},
		{name: "blank URI", uri: "   "},
		{name: "blank database", database: "   "},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("INVENTORY_GRPC_ADDR", test.address)
			t.Setenv("INVENTORY_EXPIRATION_INTERVAL", "")
			t.Setenv("MONGODB_URI", test.uri)
			t.Setenv("MONGODB_DATABASE", test.database)
			if _, err := loadConfig(); err == nil {
				t.Fatal("loadConfig() error = nil, want validation error")
			}
		})
	}
}
