FROM python:3.13-slim

ENV PYTHONDONTWRITEBYTECODE=1 PYTHONUNBUFFERED=1 PIP_NO_CACHE_DIR=1 PIP_DISABLE_PIP_VERSION_CHECK=1
WORKDIR /srv
RUN pip install --index-url https://download.pytorch.org/whl/cpu torch==2.14.1
COPY requirements.txt .
RUN pip install -r requirements.txt
COPY models ./models
COPY iahvision ./iahvision
# load the models once at build time: a broken download or weight file fails the build, not the first check-in
RUN python -c "from iahvision.matcher import Matcher; Matcher()"

USER nobody
EXPOSE 8090
HEALTHCHECK --interval=15s --timeout=5s --start-period=30s --retries=4 \
  CMD python -c "import urllib.request,sys; sys.exit(urllib.request.urlopen('http://127.0.0.1:8090/healthz', timeout=4).status != 200)"
ENTRYPOINT ["python", "-m", "iahvision.server"]
