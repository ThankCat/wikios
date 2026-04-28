const isStaticExport = process.env.NEXT_STATIC_EXPORT === "1";
const apiBaseURL = (process.env.NEXT_PUBLIC_API_BASE_URL || process.env.API_BASE_URL || "http://127.0.0.1:9025").replace(
  /\/$/,
  "",
);

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
              destination: `${apiBaseURL}/api/:path*`,
            },
            {
              source: "/healthz",
              destination: `${apiBaseURL}/healthz`,
            },
          ];
        },
      }
    : {}),
};

export default nextConfig;
