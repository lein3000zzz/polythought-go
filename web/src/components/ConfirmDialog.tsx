import type { ReactNode } from 'react'

import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from '@/components/ui/alert-dialog'

type Props = {
  open: boolean
  title: string
  description: string
  confirm: string
  cancel?: string
  destructive?: boolean
  onConfirm: () => void
  onCancel: () => void
  // A third choice, between the two.
  alternative?: string
  onAlternative?: () => void
  // More to choose, between the description and the buttons.
  children?: ReactNode
}

export function ConfirmDialog({
  open,
  title,
  description,
  confirm,
  cancel = 'Cancel',
  destructive,
  onConfirm,
  onCancel,
  alternative,
  onAlternative,
  children,
}: Props) {
  return (
    <AlertDialog open={open} onOpenChange={(isOpen) => !isOpen && onCancel()}>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>{title}</AlertDialogTitle>
          <AlertDialogDescription>{description}</AlertDialogDescription>
        </AlertDialogHeader>
        {children}
        <AlertDialogFooter>
          <AlertDialogCancel>{cancel}</AlertDialogCancel>
          {alternative && (
            <AlertDialogAction variant="outline" onClick={onAlternative}>
              {alternative}
            </AlertDialogAction>
          )}
          <AlertDialogAction variant={destructive ? 'destructive' : 'default'} onClick={onConfirm}>
            {confirm}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}
