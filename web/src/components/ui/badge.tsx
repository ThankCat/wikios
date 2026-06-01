import type { HTMLAttributes } from "react";
import { cva, type VariantProps } from "class-variance-authority";
import { cn } from "@/lib/utils";

const badgeVariants = cva(
  "inline-flex items-center rounded-md border px-2.5 py-0.5 text-xs font-semibold transition-colors",
  {
	    variants: {
	      variant: {
	        default: "border-transparent bg-secondary text-secondary-foreground hover:bg-secondary/80",
	        success: "border-transparent bg-secondary text-secondary-foreground",
	        warning: "border-transparent bg-secondary text-secondary-foreground",
	        danger: "border-transparent bg-destructive text-destructive-foreground",
	        outline: "text-foreground",
	      },
    },
    defaultVariants: {
      variant: "default",
    },
  },
);

export function Badge({
  className,
  variant,
  ...props
}: HTMLAttributes<HTMLDivElement> & VariantProps<typeof badgeVariants>) {
  return <div className={cn(badgeVariants({ variant }), className)} {...props} />;
}
