FROM node:22-alpine AS build
WORKDIR /app
ARG VITE_API_URL=http://api:8080
ENV VITE_API_URL=${VITE_API_URL}
COPY package.json package-lock.json ./
RUN npm ci
COPY . .
RUN npm run build

# Production deps only: devDependencies (vite, typescript, …) are ~500 MB
# and are not needed at runtime.
FROM node:22-alpine AS prod-deps
WORKDIR /app
COPY package.json package-lock.json ./
RUN npm ci --omit=dev && npm cache clean --force

FROM node:22-alpine
WORKDIR /app
ENV NODE_ENV=production
RUN adduser -D -u 10001 app
COPY --from=prod-deps /app/node_modules ./node_modules
COPY --from=build /app/dist ./dist
COPY --from=build /app/package.json ./package.json
USER app
EXPOSE 3000
CMD ["npm", "start"]
