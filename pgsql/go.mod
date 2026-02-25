module github.com/violin0622/robit/pgsql

go 1.25.5

replace github.com/violin0622/robit => ../

require (
	github.com/lib/pq v1.10.9
	github.com/violin0622/robit v0.0.0-00010101000000-000000000000
)

require github.com/go-logr/logr v1.4.3
