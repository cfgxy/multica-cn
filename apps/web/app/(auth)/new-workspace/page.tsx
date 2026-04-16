"use client";

import { useRouter } from "next/navigation";
import { useEffect } from "react";
import { useAuthStore } from "@multica/core/auth";
import { paths } from "@multica/core/paths";
import { CreateWorkspaceForm } from "@multica/views/workspace/create-workspace-form";

export default function NewWorkspacePage() {
  const router = useRouter();
  const user = useAuthStore((s) => s.user);
  const isLoading = useAuthStore((s) => s.isLoading);

  useEffect(() => {
    if (!isLoading && !user) router.replace(paths.login());
  }, [isLoading, user, router]);

  if (isLoading || !user) return null;

  return (
    <div className="flex min-h-svh flex-col items-center justify-center bg-background px-6 py-12">
      <div className="flex w-full max-w-md flex-col items-center gap-6">
        <div className="text-center">
          <h1 className="text-3xl font-semibold tracking-tight">
            Welcome to Multica
          </h1>
          <p className="mt-2 text-muted-foreground">
            Create your workspace to get started.
          </p>
        </div>
        <CreateWorkspaceForm
          onSuccess={(ws) => router.push(paths.workspace(ws.slug).issues())}
        />
      </div>
    </div>
  );
}
