# 使用官方 Node 镜像作为基础
FROM node:18-bullseye AS builder

WORKDIR /app
COPY package*.json ./
RUN npm install
COPY . .
RUN npm run build

# 使用 Nginx 提供静态资源服务
FROM nginx:alpine
COPY --from=builder /app/dist /usr/share/nginx/html
# 替换默认配置以支持 React Router (如果需要)
# COPY nginx.conf /etc/nginx/conf.d/default.conf

EXPOSE 80
CMD ["nginx", "-g", "daemon off;"]
