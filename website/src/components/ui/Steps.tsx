import React from 'react';
import { cn } from './lib/utils';

interface StepsProps extends React.HTMLAttributes<HTMLOListElement> {
  children: React.ReactNode;
}

function Steps({ className, children, ...props }: StepsProps) {
  return (
    <ol
      className={cn('ml-0 list-none space-y-6 pl-0', className)}
      {...props}
    >
      {children}
    </ol>
  );
}

interface StepProps extends React.HTMLAttributes<HTMLLIElement> {
  title: string;
  children?: React.ReactNode;
}

function Step({ className, title, children, ...props }: StepProps) {
  return (
    <li
      className={cn(
        'relative flex gap-4 pl-0',
        '[counter-increment:step]',
        className,
      )}
      {...props}
    >
      <div className="flex h-8 w-8 shrink-0 items-center justify-center rounded-full border border-border bg-muted text-sm font-semibold text-muted-foreground">
        {/* step number rendered via CSS counter or manually — keep it simple */}
      </div>
      <div className="flex flex-col gap-1 pt-1">
        <p className="font-semibold text-foreground">{title}</p>
        {children && (
          <div className="text-sm text-muted-foreground">{children}</div>
        )}
      </div>
    </li>
  );
}

export { Steps, Step };
