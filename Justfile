set positional-arguments
set script-interpreter := ["bash", "-euo", "pipefail"]

module := "org-search"
binary := "org-search"

[script]
generate-proto:
    proto_files=()
    while IFS= read -r file; do
      proto_files+=("$file")
    done < <(find proto -name '*.proto' -print 2>/dev/null | sort)

    if ((${#proto_files[@]} == 0)); then
      exit 0
    fi

    if ! command -v protoc >/dev/null 2>&1; then
      echo "protoc is required for generate-proto" >&2
      exit 1
    fi
    if ! command -v protoc-gen-go >/dev/null 2>&1; then
      echo "protoc-gen-go is required for generate-proto" >&2
      exit 1
    fi

    include_args=(-I proto)
    if [[ -d third_party/proto ]]; then
      include_args+=(-I third_party/proto)
    fi

    protoc "${include_args[@]}" --go_out=. --go_opt=module={{module}} "${proto_files[@]}"

build: generate-proto
    mkdir -p dist
    go build ./...
    go build -o "dist/{{binary}}" "./cmd/{{binary}}"

[script]
lint *paths:
    if ! command -v goimports >/dev/null 2>&1; then
      echo "goimports is required for lint" >&2
      exit 1
    fi

    if (($# == 0)); then
      set -- .
    fi

    goimports -w "$@"

[script]
test *paths: generate-proto
    add_package() {
      local candidate="$1"
      local package_path

      if [[ "$candidate" == . || "$candidate" == "./." ]]; then
        package_path="./..."
      elif [[ "$candidate" == *"..." ]]; then
        if [[ "$candidate" == ./* ]]; then
          package_path="$candidate"
        else
          package_path="./${candidate#./}"
        fi
      elif [[ -d "$candidate" ]]; then
        candidate="${candidate%/}"
        package_path="./${candidate#./}/..."
      elif [[ -f "$candidate" && "$candidate" == *.go ]]; then
        package_path="./$(dirname "${candidate#./}")"
      elif [[ -f "$candidate" ]]; then
        return
      else
        package_path="$candidate"
      fi

      if [[ "$package_path" == "./." ]]; then
        package_path="."
      fi

      if [[ -z "${seen[$package_path]+x}" ]]; then
        packages+=("$package_path")
        seen["$package_path"]=1
      fi
    }

    declare -A seen=()
    packages=()

    for path in "$@"; do
      add_package "$path"
    done

    if ((${#packages[@]} == 0)); then
      go test ./...
      exit 0
    fi

    go test "${packages[@]}"

[script]
run *args: build
    exec "dist/{{binary}}" "$@"

[script]
install: build
    target_dir="$(go env GOBIN)"
    if [[ -z "$target_dir" ]]; then
      target_dir="$(go env GOPATH)/bin"
    fi

    mkdir -p "$target_dir"
    cp "dist/{{binary}}" "$target_dir/{{binary}}"
