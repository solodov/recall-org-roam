set positional-arguments
set script-interpreter := ["bash", "-euo", "pipefail"]

module := "github.com/solodov/recall-org-roam"
binary := "recall-org-roam"

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

[script]
build: generate-proto
    mkdir -p dist
    go build ./...
    go build -o "dist/{{binary}}" "./cmd/{{binary}}"

    if [[ -f emacs/recall-org-roam.el ]]; then
      if ! command -v emacs >/dev/null 2>&1; then
        echo "emacs is required to build the Emacs package" >&2
        exit 1
      fi

      emacs --batch -Q -L emacs -f batch-byte-compile emacs/recall-org-roam.el
      rm -f emacs/recall-org-roam.elc
    fi

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
    should_run_emacs_tests_for_path() {
      local candidate="$1"
      case "$candidate" in
        .|./.|emacs|./emacs|emacs/*|./emacs/*|*.el|./*.el)
          return 0
          ;;
        *)
          return 1
          ;;
      esac
    }

    add_package() {
      local candidate="$1"
      local package_path

      if should_run_emacs_tests_for_path "$candidate"; then
        return
      fi

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
    run_go_tests=false
    run_emacs_tests=false

    if (($# == 0)); then
      run_go_tests=true
      run_emacs_tests=true
    fi

    for path in "$@"; do
      if should_run_emacs_tests_for_path "$path"; then
        run_emacs_tests=true
      fi
      add_package "$path"
    done

    if ((${#packages[@]} > 0)); then
      run_go_tests=true
    fi

    if [[ "$run_go_tests" == true ]]; then
      if ((${#packages[@]} == 0)); then
        go test ./...
      else
        go test "${packages[@]}"
      fi
    fi

    if [[ "$run_emacs_tests" == true ]]; then
      if ! command -v emacs >/dev/null 2>&1; then
        echo "emacs is required to run Emacs package tests" >&2
        exit 1
      fi

      emacs --batch -Q -L emacs -l emacs/recall-org-roam-test.el -f ert-run-tests-batch-and-exit
    fi

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
