/** @type {import('next').NextConfig} */
const nextConfig = {
  // Functional tests must not overwrite an operator's running development build.
  distDir: process.env.AGENTFLOW_TEST_DIST_DIR ?? ".next",
  reactStrictMode: true
};

export default nextConfig;
