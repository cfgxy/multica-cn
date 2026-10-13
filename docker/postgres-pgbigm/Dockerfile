# pg_bigm 派生 Postgres 镜像（RUYI-755）：pgvector pg17 + 源码编译 pg_bigm。
# ENTRYPOINT / docker-entrypoint 机制完全继承 base 镜像，未做任何覆盖。
FROM pgvector/pgvector:pg17

# 固定发布 tag，勿改用浮动分支头；v1.2-20250903 对应上游 commit
# 735dceba0ecdd8ac1aaaaa207226a7102b6bbd71（2026-10-13 git ls-remote 核对）。
ARG PG_BIGM_VERSION=v1.2-20250903

RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        build-essential \
        ca-certificates \
        curl \
        postgresql-server-dev-17 \
    && curl -fsSL --retry 3 --retry-delay 2 \
        "https://codeload.github.com/pgbigm/pg_bigm/tar.gz/refs/tags/${PG_BIGM_VERSION}" \
        -o /tmp/pg_bigm.tar.gz \
    && mkdir -p /tmp/pg_bigm \
    && tar -xzf /tmp/pg_bigm.tar.gz -C /tmp/pg_bigm --strip-components=1 \
    && make -C /tmp/pg_bigm USE_PGXS=1 \
    && make -C /tmp/pg_bigm USE_PGXS=1 install \
    && rm -rf /tmp/pg_bigm /tmp/pg_bigm.tar.gz \
    && apt-get purge -y --auto-remove build-essential curl \
    && apt-get clean \
    && rm -rf /var/lib/apt/lists/*

# 官方 postgres 镜像机制：initdb 以 postgresql.conf.sample 为蓝本生成新集群
# 配置，追加此行使 fresh init 即预加载 pg_bigm（CREATE EXTENSION 的硬性要求，
# 见 pg_bigm 官方文档）。已有数据目录不经过 initdb，须由部署侧启动参数补齐，
# 不在本镜像职责内。postgresql-server-dev-17 保留：pg_config 属于该包。
RUN echo "" >> /usr/share/postgresql/postgresql.conf.sample \
    && echo "# pg_bigm: preload required for CREATE EXTENSION (multica image)" >> /usr/share/postgresql/postgresql.conf.sample \
    && echo "shared_preload_libraries = 'pg_bigm'" >> /usr/share/postgresql/postgresql.conf.sample
