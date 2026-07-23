FROM ghcr.io/astral-sh/uv:python3.12-bookworm-slim

ENV PYTHONDONTWRITEBYTECODE=1 \
    PYTHONUNBUFFERED=1 \
    UV_CACHE_DIR=/tmp/uv-cache

WORKDIR /app

COPY pyproject.toml uv.lock ./
COPY README.md ./
COPY docs ./docs
COPY src ./src
COPY tests ./tests

RUN uv run python -m compileall -q src/garlicsync tests \
    && uv run ruff check src/garlicsync tests \
    && uv run pytest -q

EXPOSE 8765

ENTRYPOINT ["uv", "run"]
CMD ["save-sync", "--help"]
