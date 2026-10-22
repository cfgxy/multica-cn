import { z } from "zod";

/** Public wire contract for the Issue continuation-plan control surface. */
export const OuterLoopPlanSchema = z.object({
	state: z.string(),
  goal: z.string(),
  max_rounds: z.number().int().min(1).max(1000),
  round: z.number().int().min(0),
  wait_reason: z.string(),
}).passthrough();

export type OuterLoopPlan = z.infer<typeof OuterLoopPlanSchema>;

export const OuterLoopPlanResponseSchema = z.object({
  enabled: z.boolean(),
  has_plan: z.boolean(),
  plan: OuterLoopPlanSchema,
  issue_revision: z.number().int(),
}).passthrough();

export type OuterLoopPlanResponse = z.infer<typeof OuterLoopPlanResponseSchema>;

export const OuterLoopFireResponseSchema = z.object({
  dispatched: z.boolean(),
  reason: z.string().optional(),
  plan: OuterLoopPlanSchema.optional(),
  issue_revision: z.number().int().optional(),
}).passthrough();

export type OuterLoopFireResponse = z.infer<typeof OuterLoopFireResponseSchema>;

export interface UpdateOuterLoopPlanInput {
  enabled?: boolean;
  max_rounds?: number;
  goal?: string;
}
