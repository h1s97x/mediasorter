# MediaSorter 交叉编译构建镜像
# 预装 Go 工具链 + 三平台交叉编译所需系统库，避免每次构建重复 apt-get 安装
# 使用方式：在 .cnb.yml 中通过 docker.build 引用（镜像会被自动缓存，仅当本文件变化时重建）
FROM golang:1.26-bookworm

# 配置 GOPROXY 加速依赖下载（goproxy.cn 为国内源，海外构建自动回退 direct）
ENV GOPROXY=https://goproxy.cn,direct

# 预装交叉编译依赖
# - gcc                        Linux 本地编译
# - gcc-mingw-w64-x86-64       Windows 交叉编译（GUI 用）
# - libgl1-mesa-dev xorg-dev    Linux GUI(fyne) 所需 OpenGL/X11 系统库
# - libwayland-dev libxkbcommon-dev  Linux GUI(fyne) 所需 Wayland 系统库
# - pkg-config                  fyne 编译所需的 pkg-config
RUN apt-get update -qq \
    && apt-get install -y -qq --no-install-recommends \
        gcc \
        gcc-mingw-w64-x86-64 \
        libgl1-mesa-dev \
        xorg-dev \
        libwayland-dev \
        libxkbcommon-dev \
        pkg-config \
    && rm -rf /var/lib/apt/lists/*
