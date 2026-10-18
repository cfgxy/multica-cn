// Dashboard aggregation moved to `@multica/core/dashboard/aggregate`
// (RUYI-638) so the mobile stats screen aggregates on the exact same
// functions. This shim keeps every existing `…/dashboard/utils` import
// path working.
export * from "@multica/core/dashboard/aggregate";
