# nl rich text 轉換服務（Tiptap schema 的 server-side 執行環境）
FROM node:22-slim
ENV NODE_ENV=production
WORKDIR /app
COPY package.json package-lock.json ./
RUN npm ci --omit=dev
COPY schema.mjs server.mjs ./
USER node
EXPOSE 8082
CMD ["node", "server.mjs"]
