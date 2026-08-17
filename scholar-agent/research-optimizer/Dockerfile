FROM python:3.12-slim

ENV PYTHONDONTWRITEBYTECODE=1 \
    PYTHONUNBUFFERED=1 \
    RESEARCH_OPTIMIZER_PORT=8090 \
    RESEARCH_OPTIMIZER_DB_PATH=/data/experiences.sqlite3

WORKDIR /app
COPY research_optimizer ./research_optimizer
RUN useradd --create-home --uid 10001 optimizer \
    && mkdir -p /data \
    && chown -R optimizer:optimizer /data /app

USER optimizer
EXPOSE 8090
CMD ["python", "-m", "research_optimizer.service"]
