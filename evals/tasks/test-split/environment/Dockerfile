# The repo the agent works on, committed so a change can be diffed and Satat can take it as a
# snapshot. The image matches Satat's check-python lane.
FROM python:3.13.16-slim@sha256:cf50a4f6ac4f026458b08d5222023d700ac806410dff5e9ca8728fa44f2b850f
RUN apt-get update && apt-get install -y --no-install-recommends git \
  && rm -rf /var/lib/apt/lists/*
WORKDIR /app
COPY repo/ /app/
RUN git init -q -b main \
  && git -c user.name=Satat -c user.email=satat@localhost add -A \
  && git -c user.name=Satat -c user.email=satat@localhost commit -q -m "Base"
