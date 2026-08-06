# 1. The Dependency Diet

*Every dependency you import is a tax you pay forever, in builds, in scans, and in the shapes it forces on your code.*

## The Situation

The go.mod told a story nobody had written down. Seven direct dependencies sounded modest, but they dragged in a transitive tree with sonic, quic-go, protobuf, json-iterator, two YAML parsers, and a mock generator. A service whose job was polling Windows machines over WinRM was compiling a QUIC stack because the HTTP router had an HTTP/3 client somewhere in its graph. Each build paid for all of it, each vulnerability scan re-read all of it, and each upstream release could change behavior anywhere in it. The go.sum was over a hundred lines of hashes for libraries the product never asked for.

The cost was not only compile time. gocrypt forced its own API onto the encryption code: reflection magic that walked struct tags, plus a hand-rolled workaround because "gocrypt v1.1.0 only supports string fields" while the credential model carried a json.RawMessage payload. Viper existed to merge app.yaml with environment variables, yet the code comments insisted secrets should only ever come from the environment, which meant the yaml file held little that viper was actually needed for. The database pool configuration exposed seven tuning knobs across three separate pools that nobody had ever measured a reason to tune separately.

## The Transformation

The first move was the module file itself. Gin, viper, and gocrypt left, and with them the tree they had been holding up.

BEFORE (go.mod): `require github.com/gin-gonic/gin v1.11.0`, `github.com/spf13/viper v1.21.0`, `github.com/firdasafridi/gocrypt v1.1.0`, with indirects including `github.com/bytedance/sonic`, `github.com/quic-go/quic-go`, `google.golang.org/protobuf`, `github.com/json-iterator/go`, `github.com/goccy/go-yaml`.

AFTER (go.mod): `require github.com/golang-jwt/jwt/v4`, `github.com/jmoiron/sqlx`, `github.com/jackc/pgx/v5`. Seven direct dependencies became four and the go.sum shrank by 108 lines.

The before state shipped a JSON fast-path, a QUIC implementation, and a protobuf runtime to serve a handful of REST endpoints. The after state keeps only libraries that do something the standard library cannot, and each one is earned by a real call site.

The gocrypt replacement is the most instructive. The old code handed the whole struct to a black box and hoped.

BEFORE (pkg/api/encryption.go):

```go
aesOpt, err := gocrypt.NewAESOpt(secretKey)
...
gc := gocrypt.New(opt)
err = gc.Encrypt(&entity)
```

That call was followed by a separate reflection pass called `handleRawMessageFields`, written purely because the library's tag scanner could not handle json.RawMessage. The new code names every step of the cipher.

AFTER (pkg/api/encryption.go):

```go
func newAEAD(secretKey string) (cipher.AEAD, error) {
	if len(secretKey) != 64 {
		return nil, fmt.Errorf("encryption key must be 64 hex characters, got %d", len(secretKey))
	}
	key, err := hex.DecodeString(secretKey)
	...
	return cipher.NewGCM(block)
}
```

```go
nonce := make([]byte, aead.NonceSize())
if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
	return "", err
}
ciphertext := aead.Seal(nonce, nonce, []byte(plain), nil)
return hex.EncodeToString(ciphertext), nil
```

The old code was bad not because it was insecure, but because you could not see whether it was. The new code is good because the format is explicit, the key length is enforced at the boundary, and the comment states the critical fact: gocrypt's wire format was nonce-prefixed, hex-encoded AES, exactly what this emits, so every stored row decrypts with zero migration.

Config went the same way. Viper's layered merge was replaced by two helpers and a struct literal.

BEFORE (pkg/config/config.go):

```go
v := viper.New()
v.SetDefault("DB_HOST", "localhost")
...
v.AutomaticEnv()
v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
v.Unmarshal(&config)
```

AFTER (pkg/config/config.go):

```go
func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
```

`LoadConfig` now builds the Config struct with `env("DB_HOST", "localhost")` lines and three shared pool keys instead of seven. The before state ran a config library whose main job was merging layers the team already did not trust, and it carried a yaml file that was doomed to drift from the code. The after state keeps every default in one readable place, and the seven pool knobs collapse to three because three separately tuned pools had never been validated.

The diet was also a deletion. flow.md (186 lines), mirror.md (357 lines), progress.md, added_endpoints.txt, seed.sh, app.yaml, and a tracked app.log of 256 lines and a tracked .env all left the tree. A stale doc costs more than zero: it teaches the wrong model of the system, and a tracked log file teaches that logs belong in git.

## The Lesson

**Before you import a library, ask whether the standard library already pays that bill, because a dependency is a promise to carry its API, its bugs, and its transitive tree forever.** Go's stdlib has a router with method patterns, AES-256-GCM, and environment access, all maintained with the language and reviewed by everyone. The diet showed up in a smaller go.sum, but the real win was that the four remaining dependencies each do something the stdlib genuinely cannot, and every line of the other three systems is now code you can read.
