import * as React from "react";
import { cn } from "@/lib/utils";

const Input = React.forwardRef<HTMLInputElement, React.ComponentProps<"input">>(
  ({ className, ...props }, ref) => {
    return (
      <input
	        ref={ref}
	        className={cn(
	          "flex h-11 w-full rounded-2xl border border-input bg-background px-4 py-2 text-sm shadow-sm transition placeholder:text-muted-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring dark:shadow-none",
	          className,
	        )}
        {...props}
      />
    );
  },
);
Input.displayName = "Input";

export { Input };
