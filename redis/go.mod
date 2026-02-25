module github.com/violin0622/robit/redis

go 1.25.5

replace github.com/violin0622/robit => ../

require (
	github.com/redis/go-redis/v9 v9.17.2
	github.com/violin0622/robit v0.0.0-00010101000000-000000000000
)

require (
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/dgryski/go-rendezvous v0.0.0-20200823014737-9f7001d12a5f // indirect
	github.com/go-logr/logr v1.4.3 // indirect
)
