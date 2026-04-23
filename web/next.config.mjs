const isStaticExport = process.env.NEXT_STATIC_EXPORT === "1";

/** @type {import('next').NextConfig} */
const nextConfig = {
  reactStrictMode: true,
  ...(isStaticExport
    ? {
        output: "export",
      }
    : {}),
  ...(!isStaticExport
    ? {
        async rewrites() {
          return [
            {
              source: "/api/:path*",
              destination: "http://127.0.0.1:8080/api/:path*",
            },
            {
              source: "/healthz",
              destination: "http://127.0.0.1:8080/healthz",
            },
          ];
        },
      }
    : {}),
};

export default nextConfig;
