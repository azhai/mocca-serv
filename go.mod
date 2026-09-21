module github.com/azhai/mocca

go 1.27.1

require (
	github.com/azhai/gobus v0.2.1
	github.com/azhai/goent v0.9.9-beta.3
	github.com/cloudsoda/go-smb2 v0.0.0-20260916044308-cba813414346
	github.com/dhowden/tag v0.0.0-20240417053706-3d75831295e8
	github.com/fsnotify/fsnotify v1.10.1
	github.com/golang-jwt/jwt/v4 v4.5.2
	github.com/labstack/echo/v5 v5.3.1
	github.com/pkg/errors v0.9.1
	github.com/rwcarlsen/goexif v0.0.0-20190401172101-9e8deecbddbd
	golang.org/x/crypto v0.57.0
)

require (
	github.com/VictoriaMetrics/fastcache v1.13.3 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/cloudsoda/sddl v0.0.0-20250224235906-926454e91efc // indirect
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/fxamacker/cbor/v2 v2.9.4 // indirect
	github.com/geoffgarside/ber v1.2.0 // indirect
	github.com/golang/snappy v1.0.0 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/hashicorp/go-uuid v1.0.3 // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/pgx/v5 v5.11.0 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	github.com/jcmturner/aescts/v2 v2.0.0 // indirect
	github.com/jcmturner/dnsutils/v2 v2.0.0 // indirect
	github.com/jcmturner/gofork v1.7.6 // indirect
	github.com/jcmturner/goidentity/v6 v6.0.1 // indirect
	github.com/jcmturner/gokrb5/v8 v8.4.4 // indirect
	github.com/jcmturner/rpc/v2 v2.0.3 // indirect
	github.com/mattn/go-isatty v0.0.24 // indirect
	github.com/ncruces/go-strftime v1.0.0 // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
	github.com/x448/float16 v0.8.4 // indirect
	golang.org/x/net v0.59.0 // indirect
	golang.org/x/sync v0.23.0 // indirect
	golang.org/x/sys v0.48.0 // indirect
	golang.org/x/text v0.42.0 // indirect
	modernc.org/libc v1.76.0 // indirect
	modernc.org/mathutil v1.7.1 // indirect
	modernc.org/memory v1.12.1 // indirect
	modernc.org/sqlite v1.59.0 // indirect
)

replace (
	github.com/azhai/gobus => ../gobus
	github.com/azhai/goent => ../goent
)
