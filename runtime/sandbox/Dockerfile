# AgentLab code sandbox image: data libraries plus the stdlib-only runner.
# Build:  docker build -t agentlab/sandbox-python:20261024 runtime/sandbox
# Behind a registry or PyPI mirror, override the sources without editing this file:
#   --build-arg PYTHON_BASE=public.ecr.aws/docker/library/python:3.12-slim
#   --build-arg PIP_INDEX_URL=https://pypi.tuna.tsinghua.edu.cn/simple
# The platform starts it with no network, a read-only root and tmpfs /work and /tmp
# (see backend/internal/platform/sandbox/docker.go). Pin the base image by digest for releases.
ARG PYTHON_BASE=python:3.12-slim
FROM ${PYTHON_BASE}

ARG PIP_INDEX_URL
ENV PIP_NO_CACHE_DIR=1 \
    PIP_DISABLE_PIP_VERSION_CHECK=1 \
    PYTHONDONTWRITEBYTECODE=1 \
    MPLCONFIGDIR=/tmp/mpl \
    MPLBACKEND=Agg \
    LANG=C.UTF-8

RUN pip install pandas==2.2.3 numpy==2.1.3 openpyxl==3.1.5 matplotlib==3.9.2 \
 && useradd --uid 10001 --user-group --no-create-home --shell /usr/sbin/nologin sandbox

COPY runner.py /opt/agentlab/runner.py

USER 10001:10001
WORKDIR /work
LABEL org.agentlab.sandbox-protocol="1"
ENTRYPOINT ["python3", "-I", "-B", "/opt/agentlab/runner.py"]
