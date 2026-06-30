import React from 'react';
import type { LucideIcon } from 'lucide-react';
import { cn } from './lib/utils';

interface FeatureCardProps extends React.HTMLAttributes<HTMLDivElement> {
  icon: LucideIcon;
  title: string;
  description: string;
}

function FeatureCard({ icon: Icon, title, description, className, ...props }: FeatureCardProps) {
  return (
    <div
      className={cn(
        'flex flex-col gap-3 rounded-lg border border-border bg-card p-6',
        'transition-colors hover:border-border/80',
        className,
      )}
      {...props}
    >
      <div className="flex h-10 w-10 items-center justify-center rounded-md bg-muted">
        <Icon className="h-5 w-5 text-primary" strokeWidth={1.75} />
      </div>
      <div className="flex flex-col gap-1">
        <h3 className="text-sm font-semibold text-foreground">{title}</h3>
        <p className="text-sm text-muted-foreground leading-relaxed">{description}</p>
      </div>
    </div>
  );
}

interface FeatureGridProps extends React.HTMLAttributes<HTMLDivElement> {
  children: React.ReactNode;
  columns?: 2 | 3 | 4;
}

function FeatureGrid({ children, columns = 3, className, ...props }: FeatureGridProps) {
  const colClass = {
    2: 'grid-cols-1 sm:grid-cols-2',
    3: 'grid-cols-1 sm:grid-cols-2 lg:grid-cols-3',
    4: 'grid-cols-1 sm:grid-cols-2 lg:grid-cols-4',
  }[columns];

  return (
    <div
      className={cn('grid gap-4', colClass, className)}
      {...props}
    >
      {children}
    </div>
  );
}

export { FeatureGrid, FeatureCard };
