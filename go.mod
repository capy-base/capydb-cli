module github.com/capy-base/capydb/cli

go 1.26.5

require (
	github.com/capy-base/capydbclient v1.3.5
	github.com/spf13/cobra v1.10.2
)

require (
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/spf13/pflag v1.0.10 // indirect
)

// TODO: drop after capydbclient v1.4.0 is tagged; bump require to v1.4.0
replace github.com/capy-base/capydbclient => ../capydbclient
