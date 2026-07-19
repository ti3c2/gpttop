# Go CLI Development

- Put Go CLI code under `./src`.
- Prefer the standard library unless a dependency clearly improves the CLI.
- Format Go code with `make fmt` before finishing work. Do not rely on `gofmt` alone.
- Lint Go code with `make lint`.
- Run tests with `make test` before finishing work.
- Rebuild the project with `make build` before finishing work.

# Compatibility

This is a new, experimental repo. Do not preserve backward compatibility unless the user asks for it explicitly.

- Prefer the cleanest design over migration paths, shims, or dual code paths.
- When asked to add something new or remove something, do not keep compatibility with previous versions unless asked explicitly.
