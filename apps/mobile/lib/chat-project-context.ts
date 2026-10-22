export type ProjectContextNotice = "loading" | "error" | "empty";

export function resolveProjectContextNotice(input: {
  projectCount: number;
  hasSelectedProject: boolean;
  isPending: boolean;
  isError: boolean;
}): ProjectContextNotice | null {
  if (input.projectCount > 0 || input.hasSelectedProject) return null;
  if (input.isPending) return "loading";
  if (input.isError) return "error";
  return "empty";
}
