UV ?= uv
DOCKER ?= docker
IMAGE ?= save-sync-ps-pc:latest
UV_CACHE_DIR ?= .uv-cache
UV_RUN := UV_CACHE_DIR=$(UV_CACHE_DIR) $(UV) run
UI_HOST ?= 127.0.0.1
UI_PORT ?= 8765

PY_FILES := src/garlicsync tests

all: py-check ruff test

help:
	@printf '%s\n' \
		'Targets:' \
		'  make all              Run Python checks and tests' \
		'  make py-check         Compile-check Python bridge/service/converters/plugins' \
		'  make ruff             Run Ruff lint checks' \
		'  make test             Run Python tests' \
		'  make ui               Run local browser UI service' \
		'  make bridge-help      Show save-sync help' \
		'  make docker-build     Build PC bridge Docker image' \
		'  make docker-help      Show bridge help inside Docker image' \
		'  make docker-ui        Run browser UI inside Docker image' \
		'  make clean            Remove build/cache artifacts'

py-check:
	$(UV_RUN) python -m compileall -q src/garlicsync tests

ruff:
	$(UV_RUN) ruff check $(PY_FILES)

test:
	$(UV_RUN) pytest -q

bridge-help:
	$(UV_RUN) save-sync --help

ui:
	$(UV_RUN) save-sync-ui --host $(UI_HOST) --port $(UI_PORT)

docker-build:
	$(DOCKER) build -t $(IMAGE) .

docker-help: docker-build
	$(DOCKER) run --rm $(IMAGE) save-sync --help

docker-ui: docker-build
	$(DOCKER) run --rm -p $(UI_PORT):8765 $(IMAGE) save-sync-ui --host 0.0.0.0 --port 8765

clean:
	rm -rf __pycache__ tests/__pycache__ src/garlicsync/__pycache__ src/garlicsync/clair/__pycache__ src/garlicsync/games/__pycache__
	rm -rf src/garlic_savemgr_tools.egg-info src/save_sync_ps_pc.egg-info .pytest_cache .ruff_cache .uv-cache .venv

.PHONY: all help py-check ruff test bridge-help ui docker-build docker-help docker-ui clean
