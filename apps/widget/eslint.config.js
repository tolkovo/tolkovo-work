import { tolkovoConfig } from "@tolkovo/config/eslint";

export default tolkovoConfig({
  tsconfigRootDir: import.meta.dirname,
  project: ["./tsconfig.json", "./tsconfig.node.json"],
});
