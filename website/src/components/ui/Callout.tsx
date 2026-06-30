import React from 'react';
import { cva, type VariantProps } from 'class-variance-authority';
import { Info, AlertTriangle, CheckCircle, XCircle } from 'lucide-react';
import { cn } from './lib/utils';

const calloutVariants = cva(
  'flex gap-3 rounded-lg border p-4 text-sm my-4',
  {
    variants: {
      variant: {
        info: 'border-blue-500/30 bg-blue-500/10 text-blue-800 dark:text-blue-200',
        warning: 'border-amber-500/30 bg-amber-500/10 text-amber-800 dark:text-amber-200',
        success: 'border-green-500/30 bg-green-500/10 text-green-800 dark:text-green-200',
        danger: 'border-red-500/30 bg-red-500/10 text-red-800 dark:text-red-200',
      },
    },
    defaultVariants: {
      variant: 'info',
    },
  },
);

const icons = {
  info: Info,
  warning: AlertTriangle,
  success: CheckCircle,
  danger: XCircle,
};

type CalloutVariant = 'info' | 'warning' | 'success' | 'danger';

export interface CalloutProps
  extends Omit<React.HTMLAttributes<HTMLDivElement>, 'title'>,
    VariantProps<typeof calloutVariants> {
  /** Alias for `variant`. Both are accepted so `<Callout type="warning">` and `<Callout variant="warning">` work. */
  type?: CalloutVariant;
  title?: string;
}

function Callout({ className, variant, type, title, children, ...props }: CalloutProps) {
  const resolved: CalloutVariant = (variant as CalloutVariant) ?? type ?? 'info';
  const Icon = icons[resolved];
  return (
    <div className={cn(calloutVariants({ variant: resolved }), className)} {...props}>
      <Icon className="mt-0.5 h-4 w-4 shrink-0" strokeWidth={1.75} />
      <div className="flex flex-col gap-1">
        {title && <p className="font-semibold">{title}</p>}
        <div>{children}</div>
      </div>
    </div>
  );
}

export { Callout };
