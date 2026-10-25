# oapiclient-gen

Owned by me-myself-and-i.

## Usage

TODO — what this tool does, and the commands it offers.

```sh
make run ARGS='--help'
```

## Build

```sh
make build            # compile
make run ARGS='...'   # run without installing
```

Releases are built by CI and published to GitHub Releases;
`oapiclient-gen update` pulls the newest one.

## Testing

```sh
make test             # go vet and go test -race. What CI runs.
```

Add a service's own setup to `Makefile.local`. It is not generated, so
it survives a regenerate.

## Deploy

Not deployed — this is a binary people install, not a service. CI
attaches one per platform to a GitHub Release when the version in
`VERSION` changes.
